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
)

const Directory = ".sitebrush-update"

type Version struct {
	Version       string        `json:"version"`
	Binary        string        `json:"binary"`
	SHA256        string        `json:"sha256"`
	Compatibility Compatibility `json:"compatibility"`
}

type State struct {
	Launcher          string    `json:"launcher"`
	Arguments         []string  `json:"arguments"`
	WorkingDirectory  string    `json:"working_directory"`
	Stage             string    `json:"stage"`
	Error             string    `json:"error,omitempty"`
	Current           Version   `json:"current"`
	Previous          *Version  `json:"previous,omitempty"`
	Available         *Manifest `json:"available,omitempty"`
	ManifestHash      string    `json:"manifest_hash,omitempty"`
	HighestSequence   uint64    `json:"highest_sequence"`
	Operation         string    `json:"operation,omitempty"`
	Backup            string    `json:"backup,omitempty"`
	Candidate         *Version  `json:"candidate,omitempty"`
	WritesOpened      bool      `json:"writes_opened"`
	RollbackAvailable bool      `json:"rollback_available"`
	RollbackReason    string    `json:"rollback_reason,omitempty"`
	CheckedAt         string    `json:"checked_at,omitempty"`
	Busy              bool      `json:"busy"`
	OperationError    string    `json:"operation_error,omitempty"`
	NotificationError string    `json:"notification_error,omitempty"`
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
	_, copyErr := io.Copy(io.MultiWriter(output, digest), sessionReader{reader: input, stop: stop})
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
	if err := syncDirectory(destination, filepath.Dir(destinationName)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type backupEntry struct {
	Name   string
	Mode   fs.FileMode
	SHA256 string
}

func snapshot(storagePath string, store *os.Root, operation string, stop <-chan struct{}) (string, error) {
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
	entries := 0
	err = fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-stop:
			return errors.New("backup canceled")
		default:
		}
		if name == Directory {
			return fs.SkipDir
		}
		if name == "." {
			return nil
		}
		entries++
		if entries > 1000000 {
			return errors.New("backup entry limit exceeded")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("backup refuses special file %q", name)
		}
		record := backupEntry{Name: name, Mode: info.Mode()}
		if info.IsDir() {
			if err := destination.MkdirAll(name, info.Mode().Perm()); err != nil {
				return err
			}
			if err := syncDirectory(destination, filepath.Dir(name)); err != nil {
				return err
			}
		} else {
			record.SHA256, err = copyFileSession(source, name, destination, name, info.Mode(), stop)
			if err != nil {
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
	if err := atomicJSON(store, filepath.Join(backup, "complete.json"), struct{ SHA256 string }{indexDigest}); err != nil {
		return "", err
	}
	if err := verifySnapshot(store, backup); err != nil {
		return "", err
	}
	if err := syncAncestors(store, filepath.Join(backup, "files")); err != nil {
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
		if err := visit(entry); err != nil {
			return err
		}
	}
}

func verifySnapshot(store *os.Root, backup string) error {
	completion, err := store.ReadFile(filepath.Join(backup, "complete.json"))
	if err != nil {
		return err
	}
	var marker struct{ SHA256 string }
	if err := json.Unmarshal(completion, &marker); err != nil {
		return err
	}
	indexDigest, err := fileDigest(store, filepath.Join(backup, "index.jsonl"))
	if err != nil {
		return err
	}
	if indexDigest != marker.SHA256 {
		return errors.New("backup inventory checksum mismatch")
	}
	files, err := store.OpenRoot(filepath.Join(backup, "files"))
	if err != nil {
		return err
	}
	defer files.Close()
	return eachBackupEntry(store, backup, func(entry backupEntry) error {
		info, err := files.Lstat(entry.Name)
		if err != nil {
			return err
		}
		if info.Mode().Type() != entry.Mode.Type() || info.Mode().Perm() != entry.Mode.Perm() {
			return fmt.Errorf("backup mode mismatch: %s", entry.Name)
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
}

// Restoration is repeatable after a crash. The verified backup remains intact
// until all files have been replaced and all post-snapshot additions removed.
func restore(storagePath string, store *os.Root, backup string) error {
	if backup == "" {
		return errors.New("no verified recovery backup")
	}
	if err := verifySnapshot(store, backup); err != nil {
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
	err = eachBackupEntry(store, backup, func(entry backupEntry) error {
		if entry.Mode.IsDir() {
			if err := destination.MkdirAll(entry.Name, entry.Mode.Perm()); err != nil {
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
