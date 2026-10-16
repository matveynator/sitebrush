package appupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// BackupPolicy belongs to one accepted operation. A missing policy means all
// groups; an explicit zero policy means the owner deliberately chose no copy.
type BackupPolicy struct {
	Databases bool `json:"databases"`
	Analytics bool `json:"analytics"`
	Files     bool `json:"files"`
	Settings  bool `json:"settings"`
}

func FullBackupPolicy() BackupPolicy {
	return BackupPolicy{Databases: true, Analytics: true, Files: true, Settings: true}
}

func ParseBackupPolicy(selection string) (BackupPolicy, error) {
	if selection == "all" {
		return FullBackupPolicy(), nil
	}
	policy := BackupPolicy{}
	if selection == "none" {
		return policy, nil
	}
	for _, group := range strings.Split(selection, ",") {
		switch strings.TrimSpace(group) {
		case "databases":
			policy.Databases = true
		case "analytics":
			policy.Analytics = true
		case "files":
			policy.Files = true
		case "settings":
			policy.Settings = true
		default:
			return BackupPolicy{}, fmt.Errorf("unknown backup group %q; use all, none or databases,analytics,files,settings", group)
		}
	}
	return policy, nil
}

func decodeBackupPolicy(detail string) (BackupPolicy, error) {
	var policy BackupPolicy
	decoder := json.NewDecoder(strings.NewReader(detail))
	decoder.DisallowUnknownFields()
	if len(detail) > 512 || strings.TrimSpace(detail) == "" || strings.TrimSpace(detail) == "null" {
		return policy, errors.New("an explicit backup policy is required")
	}
	if err := decoder.Decode(&policy); err != nil {
		return policy, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return policy, errors.New("unexpected backup policy suffix")
	}
	return policy, nil
}

type backupScope struct {
	Policy  BackupPolicy        `json:"policy"`
	Control databaseBackupScope `json:"database"`
}

func (scope backupScope) trees() []string {
	trees := []string{}
	if scope.Policy.Files {
		trees = append(trees, "storage/files", "storage/static", "storage/chroot")
	}
	if scope.Policy.Settings {
		trees = append(trees, "storage/keys", "storage/letsencrypt", "storage/security", "security", "secrets", "config")
	}
	return trees
}

func (scope backupScope) contains(name string) bool {
	name = strings.ReplaceAll(name, string(os.PathSeparator), "/")
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
		return false
	}
	if scope.Control.coreContains(name) {
		return scope.Policy.Databases
	}
	if scope.Control.contains(name) {
		return scope.Policy.Analytics
	}
	for _, tree := range scope.trees() {
		if name == tree || strings.HasPrefix(name, tree+"/") {
			return true
		}
	}
	return false
}

// Each tree is opened through a directory capability; a replaced ancestor may
// never redirect a backup or recovery into another installation or host path.
func walkSelectedFiles(root *os.Root, scope backupScope, stop <-chan struct{}, visit func(string, fs.FileInfo) error) error {
	if err := scope.Control.validate(); err != nil {
		return err
	}
	if scope.Policy.Databases || scope.Policy.Analytics {
		if err := walkDatabaseFiles(root, scope.Control, stop, func(name string, info fs.FileInfo) error {
			if scope.contains(name) && visit != nil {
				return visit(name, info)
			}
			return nil
		}, scope.contains); err != nil {
			return err
		}
	}
	for _, tree := range scope.trees() {
		directory, err := databaseDirectory(root, tree)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		count := 0
		err = fs.WalkDir(directory.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			select {
			case <-stop:
				return errors.New("backup session stopped")
			default:
			}
			count++
			if count > 1000000 {
				return errors.New("backup entry limit exceeded")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			fullName := path.Join(tree, name)
			// Custom database paths can overlap a file tree. Database owners
			// still control those files, including during live preparation.
			if scope.Control.contains(fullName) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := root.Readlink(fullName)
				if err != nil {
					return err
				}
				if !safeBackupLink(fullName, target) || !scope.contains(path.Clean(path.Join(path.Dir(fullName), target))) {
					return fmt.Errorf("unsafe backup link %q", fullName)
				}
			} else if !info.Mode().IsRegular() && !info.IsDir() {
				return fmt.Errorf("unsafe backup entry %q", fullName)
			}
			if visit != nil {
				return visit(fullName, info)
			}
			return nil
		})
		directory.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
