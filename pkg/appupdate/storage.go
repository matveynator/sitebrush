package appupdate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Directory = ".sitebrush-update"

type Version struct {
	Version       string        `json:"version"`
	Binary        string        `json:"binary"`
	SHA256        string        `json:"sha256"`
	Compatibility Compatibility `json:"compatibility"`
}

type State struct {
	Launcher          string            `json:"launcher"`
	LauncherVersion   string            `json:"launcher_version,omitempty"`
	Arguments         []string          `json:"arguments"`
	WorkingDirectory  string            `json:"working_directory"`
	Stage             string            `json:"stage"`
	Error             string            `json:"error,omitempty"`
	Current           Version           `json:"current"`
	Previous          *Version          `json:"previous,omitempty"`
	Available         *Manifest         `json:"available,omitempty"`
	ManifestHash      string            `json:"manifest_hash,omitempty"`
	HighestSequence   uint64            `json:"highest_sequence"`
	Operation         string            `json:"operation,omitempty"`
	Backup            string            `json:"backup,omitempty"`
	RetainedBackup    string            `json:"retained_backup,omitempty"`
	Candidate         *Version          `json:"candidate,omitempty"`
	WritesOpened      bool              `json:"writes_opened"`
	RollbackAvailable bool              `json:"rollback_available"`
	RollbackReason    string            `json:"rollback_reason,omitempty"`
	CheckedAt         string            `json:"checked_at,omitempty"`
	Busy              bool              `json:"busy"`
	Checking          bool              `json:"checking,omitempty"`
	OperationError    string            `json:"operation_error,omitempty"`
	NotificationError string            `json:"notification_error,omitempty"`
	CleanupError      string            `json:"cleanup_error,omitempty"`
	Progress          OperationProgress `json:"progress"`
	BackupPolicy      *BackupPolicy     `json:"backup_policy,omitempty"`
	BackupGroups      []BackupGroup     `json:"backup_groups,omitempty"`
	BackupOptions     bool              `json:"backup_options,omitempty"`
	DataChanged       bool              `json:"data_changed,omitempty"`
	RecoveryVersion   *Version          `json:"recovery_version,omitempty"`
	RecoveryRequired  bool              `json:"recovery_required,omitempty"`
	NotificationCode  string            `json:"notification_code,omitempty"`
}

// Pruning uses only references from a durably acknowledged journal. It runs
// under the version-operation owner, so a new download cannot race deletion.
func pruneArtifacts(store *os.Root, state State, stop <-chan struct{}) error {
	if !state.Busy {
		return errors.New("artifact pruning requires exclusive operation ownership")
	}
	versions := make(map[string]bool)
	operations := make(map[string]bool)
	for _, binary := range []*Version{&state.Current, state.Previous, state.Candidate} {
		if binary == nil {
			continue
		}
		parts := strings.Split(filepath.ToSlash(binary.Binary), "/")
		if !filepath.IsLocal(binary.Binary) || filepath.Clean(binary.Binary) != binary.Binary || len(parts) != 3 || parts[0] != "versions" {
			return errors.New("unsafe retained binary reference")
		}
		versions[parts[1]] = true
	}
	for _, backup := range []string{state.Backup, state.RetainedBackup} {
		if backup == "" {
			continue
		}
		parts := strings.Split(filepath.ToSlash(backup), "/")
		if !filepath.IsLocal(backup) || filepath.Clean(backup) != backup || len(parts) != 2 || parts[1] != "backup" {
			return errors.New("unsafe retained backup reference")
		}
		operations[parts[0]] = true
	}
	// A manually invoked launcher can itself reside in the version tree.
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	for _, executablePath := range []string{state.Launcher, executable} {
		relative, err := filepath.Rel(store.Name(), executablePath)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) == 3 && parts[0] == "versions" {
			versions[parts[1]] = true
		}
	}
	for _, tree := range []struct {
		name string
		keep map[string]bool
	}{{".", operations}, {"versions", versions}} {
		directory, err := store.Open(tree.name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for {
			entries, readErr := directory.ReadDir(64)
			for _, entry := range entries {
				select {
				case <-stop:
					directory.Close()
					return errors.New("artifact cleanup canceled")
				default:
				}
				decoded, err := hex.DecodeString(entry.Name())
				if err != nil || len(decoded) != 16 || entry.Name() != strings.ToLower(entry.Name()) || tree.keep[entry.Name()] {
					continue
				}
				if err := store.RemoveAll(filepath.Join(tree.name, entry.Name())); err != nil {
					directory.Close()
					return err
				}
			}
			if readErr != nil {
				directory.Close()
				if readErr != io.EOF {
					return readErr
				}
				break
			}
		}
		if err := syncDirectory(store, tree.name); err != nil {
			return err
		}
	}
	return nil
}

// InstalledVersion reads metadata without creating storage. A standalone build
// must report its own compiled identity rather than an unrelated installation.
func InstalledVersion(storagePath, executable string) string {
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if err != nil {
		return ""
	}
	defer store.Close()
	state, err := loadState(store)
	if err != nil || state.Launcher == "" {
		return ""
	}
	launcher, err := os.Stat(state.Launcher)
	if err != nil {
		return ""
	}
	running, err := os.Stat(executable)
	if err != nil || !os.SameFile(launcher, running) {
		return ""
	}
	return state.Current.Version
}

func InstalledRuntime(storagePath string) ([]string, string, error) {
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if err != nil {
		return nil, "", err
	}
	defer store.Close()
	state, err := loadState(store)
	if err != nil {
		return nil, "", err
	}
	return append([]string(nil), state.Arguments...), state.WorkingDirectory, nil
}

func Identifier() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func atomicJSON(root *os.Root, name string, document any) error {
	payload, err := json.Marshal(document)
	if err != nil {
		return err
	}
	identifier, err := Identifier()
	if err != nil {
		return err
	}
	temporary := name + "." + identifier
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	return syncDirectory(root, filepath.Dir(name))
}

func syncDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	return syncDirectoryHandle(directory)
}

// A synced file is not durable if a newly created ancestor can disappear after
// power loss. Persist the entire private path before authorizing migrations.
func syncAncestors(root *os.Root, name string) error {
	for {
		if err := syncDirectory(root, name); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		name = filepath.Dir(name)
	}
}

func openStore(storagePath string) (*os.Root, error) {
	storage, err := os.OpenRoot(storagePath)
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	if info, err := storage.Lstat(Directory); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("unsafe update directory")
	}
	if err := storage.MkdirAll(Directory, 0o700); err != nil {
		return nil, err
	}
	store, err := storage.OpenRoot(Directory)
	if err != nil {
		return nil, err
	}
	if err := secureStore(store); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

func loadState(root *os.Root) (State, error) {
	file, err := root.Open("state.json")
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	var state State
	decoder := json.NewDecoder(io.LimitReader(file, manifestLimit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	if state.Current.Binary == "" || state.Current.SHA256 == "" {
		return State{}, errors.New("invalid installed version journal")
	}
	return state, nil
}

func fileDigest(root *os.Root, name string) (string, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("expected regular file")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// copyFile only operates on directory capabilities. Backup and recovery never
// resolve administrator-controlled names against the host filesystem root.
func copyFile(source *os.Root, sourceName string, destination *os.Root, destinationName string, mode fs.FileMode) (string, error) {
	return copyFileSession(source, sourceName, destination, destinationName, mode, nil)
}

type sessionReader struct {
	reader io.Reader
	stop   <-chan struct{}
}

func (reader sessionReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.stop:
		return 0, errors.New("file transfer consumer stopped")
	default:
		return reader.reader.Read(buffer)
	}
}

func copyFileSession(source *os.Root, sourceName string, destination *os.Root, destinationName string, mode fs.FileMode, stop <-chan struct{}) (string, error) {
	return copyFileProgress(source, sourceName, destination, destinationName, mode, stop, nil)
}

func copyFileProgress(source *os.Root, sourceName string, destination *os.Root, destinationName string, mode fs.FileMode, stop <-chan struct{}, counter *progressWriter) (string, error) {
	input, err := source.Open(sourceName)
	if err != nil {
		return "", err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("backup source is not a regular file")
	}
	if err := destination.MkdirAll(filepath.Dir(destinationName), 0o700); err != nil {
		return "", err
	}
	output, err := destination.OpenFile(destinationName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	var target io.Writer = io.MultiWriter(output, digest)
	if counter != nil {
		counter.output = target
		target = counter
	}
	_, copyErr := io.Copy(target, sessionReader{reader: input, stop: stop})
	if copyErr != nil {
		output.Close()
		return "", copyErr
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	if err := syncAncestors(destination, filepath.Dir(destinationName)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type backupEntry struct {
	Name   string
	Mode   fs.FileMode
	SHA256 string
	Link   string `json:",omitempty"`
}

// Legacy full-tree recovery may contain publication links such as latest -> 42.
// Their validation remains bounded by the old snapshot and private-store policy.
func safeBackupLink(name, target string) bool {
	if !filepath.IsLocal(target) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(name), target))
	return filepath.IsLocal(resolved) && resolved != Directory && !strings.HasPrefix(resolved, Directory+string(filepath.Separator))
}

func snapshot(storagePath string, store *os.Root, operation string, stop <-chan struct{}) (string, error) {
	return snapshotProgress(storagePath, store, operation, stop, nil, WorkProgress{})
}

func snapshotProgress(storagePath string, store *os.Root, operation string, stop <-chan struct{}, updates chan<- WorkProgress, totals WorkProgress, scopes ...databaseBackupScope) (string, error) {
	scope := defaultDatabaseBackupScope()
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	counter := &progressWriter{updates: updates, stop: stop, work: totals}
	if err := counter.report(); err != nil {
		return "", err
	}
	backup := filepath.Join(operation, "backup")
	if err := store.MkdirAll(filepath.Join(backup, "files"), 0o700); err != nil {
		return "", err
	}
	source, err := os.OpenRoot(storagePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	destination, err := store.OpenRoot(filepath.Join(backup, "files"))
	if err != nil {
		return "", err
	}
	defer destination.Close()
	index, err := store.OpenFile(filepath.Join(backup, "index.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer index.Close()
	encoder := json.NewEncoder(index)
	err = walkDatabaseFiles(source, scope, stop, func(name string, info fs.FileInfo) error {
		record := backupEntry{Name: name, Mode: info.Mode()}
		record.SHA256, err = copyFileProgress(source, name, destination, name, info.Mode(), stop, counter)
		if err != nil {
			return err
		}
		counter.work.Files++
		if time.Since(counter.last) >= time.Second {
			if err := counter.report(); err != nil {
				return err
			}
		}
		return encoder.Encode(record)
	})
	if err != nil {
		return "", err
	}
	if err := index.Sync(); err != nil {
		return "", err
	}
	if err := index.Close(); err != nil {
		return "", err
	}
	indexDigest, err := fileDigest(store, filepath.Join(backup, "index.jsonl"))
	if err != nil {
		return "", err
	}
	if err := atomicJSON(store, filepath.Join(backup, "complete.json"), snapshotMarker{DatabaseSHA256: indexDigest, DatabasePath: scope.Control}); err != nil {
		return "", err
	}
	if err := verifySnapshot(store, backup); err != nil {
		return "", err
	}
	if err := syncAncestors(store, filepath.Join(backup, "files")); err != nil {
		return "", err
	}
	if err := counter.report(); err != nil {
		return "", err
	}
	return backup, nil
}

func eachBackupEntry(store *os.Root, backup string, visit func(backupEntry) error) error {
	file, err := store.Open(filepath.Join(backup, "index.jsonl"))
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 512<<20))
	for count := 0; ; count++ {
		var entry backupEntry
		err := decoder.Decode(&entry)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if count >= 1000000 || !fs.ValidPath(entry.Name) || entry.Name == "." || entry.Name == Directory || strings.HasPrefix(entry.Name, Directory+"/") {
			return errors.New("invalid backup inventory")
		}
		if entry.Mode&os.ModeSymlink != 0 {
			if !safeBackupLink(entry.Name, entry.Link) {
				return errors.New("invalid backup link")
			}
		} else if entry.Link != "" {
			return errors.New("unexpected backup link target")
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
}

// DatabaseSHA256 intentionally has a different name from the legacy full-tree
// marker. Older supervisors must fail verification rather than delete static
// files absent from a database-only inventory during their full-tree restore.
type snapshotMarker struct {
	SHA256         string       `json:",omitempty"`
	DatabaseSHA256 string       `json:",omitempty"`
	DatabasePath   string       `json:",omitempty"`
	SelectedSHA256 string       `json:",omitempty"`
	SelectedScope  *backupScope `json:",omitempty"`
}

func verifySnapshot(store *os.Root, backup string) error {
	_, err := checkedSnapshot(store, backup)
	return err
}

func checkedSnapshot(store *os.Root, backup string) (snapshotMarker, error) {
	marker := snapshotMarker{}
	completion, err := store.ReadFile(filepath.Join(backup, "complete.json"))
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(completion, &marker); err != nil {
		return marker, err
	}
	indexDigest, err := fileDigest(store, filepath.Join(backup, "index.jsonl"))
	if err != nil {
		return marker, err
	}
	expected := marker.SHA256
	scope := databaseBackupScope{Control: marker.DatabasePath}
	if marker.DatabaseSHA256 != "" {
		if marker.SHA256 != "" {
			return marker, errors.New("ambiguous backup inventory marker")
		}
		if err := scope.validate(); err != nil {
			return marker, err
		}
		expected = marker.DatabaseSHA256
	}
	if marker.SelectedSHA256 != "" || marker.SelectedScope != nil {
		if marker.SelectedSHA256 == "" || marker.SelectedScope == nil || marker.SHA256 != "" || marker.DatabaseSHA256 != "" {
			return marker, errors.New("ambiguous selected backup marker")
		}
		if err := marker.SelectedScope.Control.validate(); err != nil {
			return marker, err
		}
		expected = marker.SelectedSHA256
	}
	if indexDigest != expected {
		return marker, errors.New("backup inventory checksum mismatch")
	}
	files, err := store.OpenRoot(filepath.Join(backup, "files"))
	if err != nil {
		return marker, err
	}
	defer files.Close()
	err = eachBackupEntry(store, backup, func(entry backupEntry) error {
		if marker.SelectedScope != nil && !marker.SelectedScope.contains(entry.Name) {
			return errors.New("entry outside selected backup groups")
		}
		if marker.SelectedScope != nil && entry.Link != "" && !marker.SelectedScope.contains(filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(entry.Name), entry.Link)))) {
			return errors.New("link outside selected backup groups")
		}
		if marker.DatabaseSHA256 != "" && (!entry.Mode.IsRegular() || entry.Link != "" || !scope.contains(entry.Name)) {
			return errors.New("non-database entry in database backup")
		}
		info, err := files.Lstat(entry.Name)
		if err != nil {
			return err
		}
		if info.Mode().Type() != entry.Mode.Type() || info.Mode().Perm() != entry.Mode.Perm() {
			return fmt.Errorf("backup mode mismatch: %s", entry.Name)
		}
		if entry.Mode&os.ModeSymlink != 0 {
			target, err := files.Readlink(entry.Name)
			if err != nil {
				return err
			}
			if target != entry.Link {
				return errors.New("backup link target mismatch")
			}
			if _, err := files.Stat(entry.Name); err != nil {
				return err
			}
		}
		if entry.Mode.IsRegular() {
			digest, err := fileDigest(files, entry.Name)
			if err != nil {
				return err
			}
			if digest != entry.SHA256 {
				return fmt.Errorf("backup checksum mismatch: %s", entry.Name)
			}
		}
		return nil
	})
	return marker, err
}

// Restoration is repeatable after a crash. The verified backup remains intact
// until the selected database files have been replaced. Legacy full-tree
// snapshots retain their original recovery contract for interrupted upgrades.
func restore(storagePath string, store *os.Root, backup string) error {
	if backup == "" {
		return errors.New("no verified recovery backup")
	}
	marker, err := checkedSnapshot(store, backup)
	if err != nil {
		return err
	}
	source, err := store.OpenRoot(filepath.Join(backup, "files"))
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenRoot(storagePath)
	if err != nil {
		return err
	}
	defer destination.Close()
	if marker.DatabaseSHA256 != "" {
		if err := walkDatabaseFiles(destination, databaseBackupScope{Control: marker.DatabasePath}, nil, nil); err != nil {
			return err
		}
	}
	if marker.SelectedScope != nil {
		if err := walkSelectedFiles(destination, *marker.SelectedScope, nil, nil); err != nil {
			return err
		}
	}
	err = eachBackupEntry(store, backup, func(entry backupEntry) error {
		if marker.SelectedScope != nil {
			if existing, err := destination.Lstat(entry.Name); err == nil && existing.Mode().Type() != entry.Mode.Type() {
				if err := destination.RemoveAll(entry.Name); err != nil {
					return err
				}
			}
		}
		if entry.Mode.IsDir() {
			if err := destination.MkdirAll(entry.Name, entry.Mode.Perm()); err != nil {
				return err
			}
			if marker.SelectedScope != nil {
				if err := destination.Chmod(entry.Name, entry.Mode.Perm()); err != nil {
					return err
				}
			}
			return syncDirectory(destination, filepath.Dir(entry.Name))
		}
		if entry.Mode&os.ModeSymlink != 0 {
			identifier, err := Identifier()
			if err != nil {
				return err
			}
			temporary := entry.Name + ".restore-" + identifier
			if err := destination.Symlink(entry.Link, temporary); err != nil {
				return err
			}
			if err := destination.Rename(temporary, entry.Name); err != nil {
				destination.Remove(temporary)
				return err
			}
			return syncDirectory(destination, filepath.Dir(entry.Name))
		}
		if digest, err := fileDigest(destination, entry.Name); err == nil && digest == entry.SHA256 {
			return nil
		}
		identifier, err := Identifier()
		if err != nil {
			return err
		}
		temporary := entry.Name + ".restore-" + identifier
		if _, err := copyFile(source, entry.Name, destination, temporary, entry.Mode); err != nil {
			return err
		}
		if err := destination.Rename(temporary, entry.Name); err != nil {
			destination.Remove(temporary)
			return err
		}
		return syncDirectory(destination, filepath.Dir(entry.Name))
	})
	if err != nil {
		return err
	}
	if marker.DatabaseSHA256 != "" {
		return walkDatabaseFiles(destination, databaseBackupScope{Control: marker.DatabasePath}, nil, func(name string, info fs.FileInfo) error {
			if _, err := source.Lstat(name); errors.Is(err, os.ErrNotExist) {
				if err := destination.Remove(name); err != nil {
					return err
				}
				return syncDirectory(destination, filepath.Dir(name))
			} else {
				return err
			}
		})
	}
	if marker.SelectedScope != nil {
		var extraDirectories []string
		err := walkSelectedFiles(destination, *marker.SelectedScope, nil, func(name string, info fs.FileInfo) error {
			if _, err := source.Lstat(name); errors.Is(err, os.ErrNotExist) {
				if info.IsDir() {
					extraDirectories = append(extraDirectories, name)
					return nil
				}
				if err := destination.Remove(name); err != nil {
					return err
				}
				return syncDirectory(destination, filepath.Dir(name))
			} else {
				return err
			}
		})
		if err != nil {
			return err
		}
		for index := len(extraDirectories) - 1; index >= 0; index-- {
			directory, err := destination.Open(extraDirectories[index])
			if err != nil {
				return err
			}
			entries, readErr := directory.ReadDir(1)
			directory.Close()
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			if len(entries) > 0 {
				continue
			}
			if err := destination.Remove(extraDirectories[index]); err != nil {
				return err
			}
		}
		return nil
	}
	return fs.WalkDir(destination.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == Directory {
			return fs.SkipDir
		}
		if name == "." {
			return nil
		}
		if _, err := source.Lstat(name); errors.Is(err, os.ErrNotExist) {
			if err := destination.RemoveAll(name); err != nil {
				return err
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
		} else if err != nil {
			return err
		}
		return nil
	})
}

func canRollback(previous, current Version) bool {
	return current.Compatibility.WritesCompatible && previous.Compatibility.ReadsFrom <= current.Compatibility.Generation && previous.Compatibility.ReadsThrough >= current.Compatibility.Generation
}

func readLimitedFile(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, errors.New("update metadata exceeds its size limit")
	}
	return payload, nil
}

// Old selected children do not understand the private-backend flag. A launcher
// may enable persistent listeners only after that child's capability is known.
func PublicBoundarySupported(storagePath, version string) bool {
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	defer store.Close()
	state, err := loadState(store)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	if state.Current.Version == version {
		return true
	}
	_, err = store.Stat(filepath.Join("public-capabilities", state.Current.Version))
	return err == nil
}
func ReportPublicBoundary(storagePath, version string) error {
	if strings.ContainsAny(version, "/\\") || version == "" || version == "." || version == ".." {
		return errors.New("invalid public boundary version")
	}
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.MkdirAll("public-capabilities", 0700); err != nil {
		return err
	}
	file, err := store.OpenFile(filepath.Join("public-capabilities", version), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	return file.Close()
}
