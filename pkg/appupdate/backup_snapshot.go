package appupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupGroup struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

type backupInspection struct {
	Work   WorkProgress
	Groups []BackupGroup
}

func inspectSelectedSpace(storagePath string, scope backupScope, stagedBytes int64, updates chan<- WorkProgress, stop <-chan struct{}) (backupInspection, error) {
	inspection := backupInspection{Groups: []BackupGroup{{Name: "databases"}, {Name: "analytics"}, {Name: "files"}, {Name: "settings"}}}
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return inspection, err
	}
	defer root.Close()
	last := time.Now()
	err = walkSelectedFiles(root, scope, stop, func(name string, info fs.FileInfo) error {
		if info.IsDir() {
			return nil
		}
		if info.Size() < 0 || info.Size() > (1<<62)-inspection.Work.TotalBytes {
			return errors.New("backup size overflow")
		}
		inspection.Work.TotalFiles++
		inspection.Work.TotalBytes += info.Size()
		group := 3
		if scope.Control.contains(name) {
			group = 0
			if !scope.Control.coreContains(name) {
				group = 1
			}
		} else if strings.HasPrefix(name, "storage/analytics/") {
			group = 1
		} else if (backupScope{Policy: BackupPolicy{Files: true}, Control: scope.Control}).contains(name) {
			group = 2
		}
		inspection.Groups[group].Files++
		inspection.Groups[group].Bytes += info.Size()
		if time.Since(last) >= time.Second {
			last = time.Now()
			return sendWork(updates, inspection.Work, stop)
		}
		return nil
	})
	if err != nil {
		return inspection, err
	}
	if err := sendWork(updates, inspection.Work, stop); err != nil {
		return inspection, err
	}
	available, err := availableSpace(storagePath)
	if err != nil {
		return inspection, err
	}
	// The staged copy already consumes space. Reserve its final size once more
	// for repeatable recovery, rather than demanding a third complete copy.
	required := uint64(32<<20) + uint64(inspection.Work.TotalBytes)*2
	if stagedBytes > inspection.Work.TotalBytes {
		stagedBytes = inspection.Work.TotalBytes
	}
	if stagedBytes > 0 {
		required -= uint64(stagedBytes)
	}
	if available < required {
		return inspection, fmt.Errorf("selected backup and recovery require %d free bytes for %d files (%d bytes), have %d", required, inspection.Work.TotalFiles, inspection.Work.TotalBytes, available)
	}
	return inspection, nil
}

type stagedBackupEntry struct {
	Info   fs.FileInfo
	Record backupEntry
}

// Ownership of this inventory is transferred from the preparation worker to
// the finalization worker. No process keeps a writable alias after transfer.
type stagedBackup struct {
	Path    string
	Scope   backupScope
	Entries map[string]stagedBackupEntry
	Bytes   int64
}

func prepareSelectedBackup(storagePath string, store *os.Root, operation string, scope backupScope, totals WorkProgress, updates chan<- WorkProgress, stop <-chan struct{}) (*stagedBackup, error) {
	staged := &stagedBackup{Path: filepath.Join(operation, "backup"), Scope: scope, Entries: make(map[string]stagedBackupEntry)}
	if scope.Policy == (BackupPolicy{}) {
		staged.Path = ""
		return staged, nil
	}
	if err := store.MkdirAll(filepath.Join(staged.Path, "files"), 0700); err != nil {
		return nil, err
	}
	// Live databases are never copied as ordinary files. Their owners must
	// finish checkpointing and close before the final copy can become valid.
	liveScope := scope
	liveScope.Policy.Databases = false
	liveScope.live = true
	if err := staged.copySelected(storagePath, store, liveScope, false, totals, updates, stop); err != nil {
		return nil, err
	}
	return staged, nil
}

func sameBackupSource(before, after fs.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime() == after.ModTime() && before.Mode() == after.Mode()
}

func (staged *stagedBackup) copySelected(storagePath string, store *os.Root, scope backupScope, final bool, totals WorkProgress, updates chan<- WorkProgress, stop <-chan struct{}) error {
	source, err := os.OpenRoot(storagePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := store.OpenRoot(filepath.Join(staged.Path, "files"))
	if err != nil {
		return err
	}
	defer destination.Close()
	counter := &progressWriter{updates: updates, stop: stop, work: totals}
	seen := make(map[string]bool)
	err = walkSelectedFiles(source, scope, stop, func(name string, info fs.FileInfo) error {
		seen[name] = true
		previous, exists := staged.Entries[name]
		if info.IsDir() {
			if exists && !previous.Info.IsDir() {
				if err := destination.Remove(name); err != nil {
					return err
				}
				staged.Bytes -= previous.Info.Size()
			}
			if err := destination.MkdirAll(name, info.Mode().Perm()); err != nil {
				return err
			}
			if err := destination.Chmod(name, info.Mode().Perm()); err != nil {
				return err
			}
			staged.Entries[name] = stagedBackupEntry{Info: info, Record: backupEntry{Name: name, Mode: info.Mode()}}
			return nil
		}
		if exists && sameBackupSource(previous.Info, info) {
			counter.work.Bytes += info.Size()
			counter.work.Files++
			if time.Since(counter.last) >= time.Second {
				return counter.report()
			}
			return nil
		}
		if exists {
			if err := destination.RemoveAll(name); err != nil {
				return err
			}
			if !previous.Info.IsDir() {
				staged.Bytes -= previous.Info.Size()
			}
			if previous.Info.IsDir() {
				for childName, child := range staged.Entries {
					if strings.HasPrefix(childName, name+"/") {
						if !child.Info.IsDir() {
							staged.Bytes -= child.Info.Size()
						}
						delete(staged.Entries, childName)
					}
				}
			}
			delete(staged.Entries, name)
		}
		record := backupEntry{Name: name, Mode: info.Mode()}
		if info.Mode()&os.ModeSymlink != 0 {
			record.Link, err = source.Readlink(name)
			if err == nil {
				err = destination.MkdirAll(filepath.Dir(name), 0700)
			}
			if err == nil {
				err = destination.Symlink(record.Link, name)
			}
			if err == nil {
				err = syncAncestors(destination, filepath.Dir(name))
				counter.work.Bytes += info.Size()
			}
		} else {
			record.SHA256, err = copyFileProgress(source, name, destination, name, info.Mode(), stop, counter)
		}
		if err != nil {
			if !final && errors.Is(err, os.ErrNotExist) {
				_ = destination.Remove(name)
				return nil
			}
			return err
		}
		after, err := source.Lstat(name)
		if err != nil || !sameBackupSource(info, after) {
			if removeErr := destination.Remove(name); removeErr != nil {
				return removeErr
			}
			if final {
				return fmt.Errorf("backup source changed after writers stopped: %s", name)
			}
			// The final pass retries files that changed during live preparation.
			return nil
		}
		if record.Mode.IsRegular() {
			digest, err := fileDigest(destination, name)
			if err != nil {
				return err
			}
			if digest != record.SHA256 {
				return fmt.Errorf("staged backup checksum mismatch: %s", name)
			}
		}
		staged.Entries[name] = stagedBackupEntry{Info: after, Record: record}
		staged.Bytes += info.Size()
		counter.work.Files++
		if time.Since(counter.last) >= time.Second {
			return counter.report()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if final {
		for name, previous := range staged.Entries {
			if !seen[name] {
				if err := destination.RemoveAll(name); err != nil {
					return err
				}
				delete(staged.Entries, name)
				if !previous.Info.IsDir() {
					staged.Bytes -= previous.Info.Size()
				}
			}
		}
	}
	return counter.report()
}

func (staged *stagedBackup) finish(storagePath string, store *os.Root, totals WorkProgress, updates chan<- WorkProgress, stop <-chan struct{}) (string, error) {
	if staged.Path == "" {
		return "", nil
	}
	if err := staged.copySelected(storagePath, store, staged.Scope, true, totals, updates, stop); err != nil {
		return "", err
	}
	index, err := store.OpenFile(filepath.Join(staged.Path, "index.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer index.Close()
	names := make([]string, 0, len(staged.Entries))
	for name := range staged.Entries {
		names = append(names, name)
	}
	sort.Strings(names)
	encoder := json.NewEncoder(index)
	for _, name := range names {
		if err := encoder.Encode(staged.Entries[name].Record); err != nil {
			return "", err
		}
		if staged.Entries[name].Record.Link != "" {
			files, err := store.OpenRoot(filepath.Join(staged.Path, "files"))
			if err != nil {
				return "", err
			}
			_, err = files.Stat(name)
			files.Close()
			if err != nil {
				return "", err
			}
		}
	}
	if err := index.Sync(); err != nil {
		return "", err
	}
	if err := index.Close(); err != nil {
		return "", err
	}
	digest, err := fileDigest(store, filepath.Join(staged.Path, "index.jsonl"))
	if err != nil {
		return "", err
	}
	marker := snapshotMarker{SelectedSHA256: digest, SelectedScope: &staged.Scope}
	if err := syncAncestors(store, filepath.Join(staged.Path, "files")); err != nil {
		return "", err
	}
	if err := atomicJSON(store, filepath.Join(staged.Path, "complete.json"), marker); err != nil {
		return "", err
	}
	return staged.Path, nil
}
