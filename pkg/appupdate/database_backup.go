package appupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The backup follows the application's database owners, rather than scanning
// published sites and release archives for files that happen to end in .db.
type databaseBackupScope struct {
	Control string
}

func defaultDatabaseBackupScope() databaseBackupScope {
	return databaseBackupScope{Control: "storage/db/sitebrush.db"}
}

func updateDatabaseBackupScope(storagePath, directory string, arguments []string) (databaseBackupScope, error) {
	scope := defaultDatabaseBackupScope()
	for index, argument := range arguments {
		configured := ""
		if argument == "-db-path" && index+1 < len(arguments) {
			configured = arguments[index+1]
		} else if strings.HasPrefix(argument, "-db-path=") {
			configured = strings.TrimPrefix(argument, "-db-path=")
		}
		if configured == "" {
			continue
		}
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(directory, configured)
		}
		absoluteRoot, err := filepath.Abs(storagePath)
		if err != nil {
			return scope, err
		}
		absoluteDatabase, err := filepath.Abs(configured)
		if err != nil {
			return scope, err
		}
		relative, err := filepath.Rel(absoluteRoot, absoluteDatabase)
		if err != nil {
			return scope, err
		}
		scope.Control = filepath.ToSlash(relative)
	}
	return scope, scope.validate()
}

func (scope databaseBackupScope) validate() error {
	if !fs.ValidPath(scope.Control) || scope.Control == "." || scope.Control == Directory || strings.HasPrefix(scope.Control, Directory+"/") || strings.Contains(scope.Control, "\\") {
		return errors.New("update database must remain inside the installation")
	}
	return nil
}

func databaseBaseName(name string) string {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func databaseName(name string) bool {
	base := strings.ToLower(databaseBaseName(name))
	return strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".sqlite") || strings.HasSuffix(base, ".sqlite3")
}

func (scope databaseBackupScope) coreContains(name string) bool {
	if name == scope.Control || databaseBaseName(name) == scope.Control {
		return true
	}
	sites := path.Join(path.Dir(scope.Control), "sites")
	if path.Dir(name) == sites && databaseName(path.Base(name)) {
		return true
	}
	if strings.HasPrefix(name, sites+"/") {
		relative := strings.TrimPrefix(databaseBaseName(name), sites+"/")
		components := strings.Split(relative, "/")
		if len(components) == 2 && safeSiteID(components[0]) && (components[1] == "content.db" || components[1] == "control.db") {
			return true
		}
	}
	return false
}

func (scope databaseBackupScope) contains(name string) bool {
	return scope.coreContains(name) || scope.analyticsContains(name)
}

func safeSiteID(identifier string) bool {
	if len(identifier) != 32 {
		return false
	}
	return strings.Trim(identifier, "0123456789abcdef") == ""
}

func (scope databaseBackupScope) analyticsContains(name string) bool {
	if strings.HasPrefix(name, "storage/analytics/") && databaseName(path.Base(name)) {
		return true
	}
	sites := path.Join(path.Dir(scope.Control), "sites") + "/"
	if !strings.HasPrefix(name, sites) {
		return false
	}
	components := strings.Split(strings.TrimPrefix(databaseBaseName(name), sites), "/")
	return len(components) == 3 && safeSiteID(components[0]) && components[1] == "analytics" && components[2] == "analytics.db"
}

// Directory capabilities may not traverse links, even links that currently
// resolve inside storage: database ownership must not silently move elsewhere.
func databaseDirectory(root *os.Root, name string) (*os.Root, error) {
	if name != "." {
		prefix := ""
		for _, component := range strings.Split(name, "/") {
			prefix = path.Join(prefix, component)
			info, err := root.Lstat(prefix)
			if err != nil {
				return nil, err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("unsafe database directory %q", prefix)
			}
		}
	}
	return root.OpenRoot(name)
}

func walkDatabaseFiles(root *os.Root, scope databaseBackupScope, stop <-chan struct{}, visit func(string, fs.FileInfo) error, selections ...BackupPolicy) error {
	select {
	case <-stop:
		return errors.New("database backup session stopped")
	default:
	}
	if err := scope.validate(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	inspect := func(name string) error {
		if len(selections) > 0 {
			selected := selections[0].Databases
			if !scope.coreContains(name) {
				selected = selections[0].Analytics
			}
			if !selected {
				return nil
			}
		}
		select {
		case <-stop:
			return errors.New("database backup session stopped")
		default:
		}
		if seen[name] {
			return nil
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe database backup entry %q", name)
		}
		seen[name] = true
		if len(seen) > 1000000 {
			return errors.New("database backup entry limit exceeded")
		}
		if visit != nil {
			return visit(name, info)
		}
		return nil
	}
	var controlDirectory *os.Root
	var err error
	if len(selections) == 0 || selections[0].Databases {
		controlDirectory, err = databaseDirectory(root, path.Dir(scope.Control))
	} else {
		err = os.ErrNotExist
	}
	if err == nil {
		controlDirectory.Close()
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if err := inspect(scope.Control + suffix); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, directoryName := range []string{path.Join(path.Dir(scope.Control), "sites"), "storage/analytics"} {
		if len(selections) > 0 {
			selected := selections[0].Databases
			if directoryName == "storage/analytics" {
				selected = selections[0].Analytics
			}
			if directoryName != "storage/analytics" {
				selected = selections[0].Databases || selections[0].Analytics
			}
			if !selected {
				continue
			}
		}
		directory, err := databaseDirectory(root, directoryName)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = fs.WalkDir(directory.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			select {
			case <-stop:
				return errors.New("database backup session stopped")
			default:
			}
			if name == "." {
				return nil
			}
			fullName := path.Join(directoryName, name)
			if entry.IsDir() {
				if scope.contains(fullName) {
					return fmt.Errorf("unsafe database backup entry %q", fullName)
				}
				if directoryName != "storage/analytics" {
					components := strings.Split(name, "/")
					if !safeSiteID(components[0]) || len(components) > 2 || len(components) == 2 && components[1] != "analytics" {
						return fs.SkipDir
					}
				}
				return nil
			}
			if scope.contains(fullName) {
				return inspect(fullName)
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
