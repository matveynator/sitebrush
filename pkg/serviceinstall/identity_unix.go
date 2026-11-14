//go:build !windows

package serviceinstall

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func ServiceUserName() string {
	if runtime.GOOS == "openbsd" || runtime.GOOS == "darwin" {
		return "_sitebrush"
	}
	return "sitebrush"
}

func prepareServiceIdentity(plan installPlan) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("installing a system service requires root privileges")
	}
	if err := checkServiceStoragePath(installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath())); err != nil {
		return err
	}
	name := ServiceUserName()
	if account, err := user.Lookup(name); err == nil {
		if account.Uid == "0" {
			return fmt.Errorf("service account %s must not be root", name)
		}
		return nil
	}
	var commands [][]string
	switch runtime.GOOS {
	case "linux":
		commands = [][]string{{"useradd", "--system", "--no-create-home", "--home-dir", "/var/empty", "--shell", "/usr/sbin/nologin", name}}
	case "freebsd":
		commands = [][]string{{"pw", "groupadd", name}, {"pw", "useradd", name, "-g", name, "-d", "/nonexistent", "-s", "/usr/sbin/nologin"}}
	case "openbsd", "netbsd":
		commands = [][]string{{"groupadd", name}, {"useradd", "-g", name, "-d", "/nonexistent", "-s", "/sbin/nologin", name}}
	case "darwin":
		return createDarwinServiceIdentity(name)
	default:
		return fmt.Errorf("service account creation is unsupported on %s", runtime.GOOS)
	}
	for _, command := range commands {
		if (command[0] == "groupadd" || command[0] == "pw" && len(command) > 1 && command[1] == "groupadd") && serviceGroupExists(name) {
			continue
		}
		output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("create service account: %s: %w: %s", strings.Join(command, " "), err, strings.TrimSpace(string(output)))
		}
	}
	if _, err := user.Lookup(name); err != nil {
		return fmt.Errorf("service account %s is unavailable after creation: %w", name, err)
	}
	return nil
}

func serviceGroupExists(name string) bool {
	_, err := user.LookupGroup(name)
	return err == nil
}

func createDarwinServiceIdentity(name string) error {
	output, err := exec.Command("dscl", ".", "-list", "/Users", "UniqueID").Output()
	if err != nil {
		return fmt.Errorf("list macOS users: %w", err)
	}
	groupOutput, err := exec.Command("dscl", ".", "-list", "/Groups", "PrimaryGroupID").Output()
	if err != nil {
		return fmt.Errorf("list macOS groups: %w", err)
	}
	used := make(map[string]bool)
	for _, listing := range []string{string(output), string(groupOutput)} {
		for _, line := range strings.Split(listing, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				used[fields[1]] = true
			}
		}
	}
	uid := ""
	for candidate := 400; candidate < 500; candidate++ {
		text := fmt.Sprint(candidate)
		if !used[text] {
			uid = text
			break
		}
	}
	if uid == "" {
		return fmt.Errorf("no available macOS system user ID")
	}
	userRecord := "/Users/" + name
	groupRecord := "/Groups/" + name
	commands := [][]string{
		{"dscl", ".", "-create", groupRecord},
		{"dscl", ".", "-create", groupRecord, "PrimaryGroupID", uid},
		{"dscl", ".", "-create", userRecord},
		{"dscl", ".", "-create", userRecord, "UniqueID", uid},
		{"dscl", ".", "-create", userRecord, "PrimaryGroupID", uid},
		{"dscl", ".", "-create", userRecord, "UserShell", "/usr/bin/false"},
		{"dscl", ".", "-create", userRecord, "NFSHomeDirectory", "/var/empty"},
	}
	for _, command := range commands {
		if output, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("create macOS service account: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func checkServiceStoragePath(storagePath string) error {
	if !filepath.IsAbs(storagePath) || filepath.Clean(storagePath) == string(filepath.Separator) {
		return fmt.Errorf("storage folder must be an absolute non-root path")
	}
	return nil
}

func prepareServiceStorage(plan installPlan) error {
	storagePath := installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath())
	if err := checkServiceStoragePath(storagePath); err != nil {
		return err
	}
	account, err := user.Lookup(ServiceUserName())
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	if uid < 0 {
		return fmt.Errorf("service user ID must be nonnegative")
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	if gid < 0 {
		return fmt.Errorf("service group ID must be nonnegative")
	}
	storageFD, err := openServiceStoragePath(storagePath, uid, gid)
	if err != nil {
		return err
	}
	defer unix.Close(storageFD)
	workingDirectory := filepath.Clean(plan.WorkingDir)
	relativeWorkingDirectory, err := filepath.Rel(filepath.Clean(storagePath), workingDirectory)
	if err != nil || relativeWorkingDirectory == ".." || strings.HasPrefix(relativeWorkingDirectory, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeWorkingDirectory) {
		return fmt.Errorf("working folder must be inside storage folder")
	}
	if relativeWorkingDirectory != "." {
		if err := ensureServiceWorkingDirectory(storageFD, relativeWorkingDirectory); err != nil {
			return err
		}
	}
	dataFD, err := ensureServiceDirectory(storageFD, "storage")
	if err != nil {
		return err
	}
	dbFD, err := ensureServiceDirectory(dataFD, "db")
	unix.Close(dataFD)
	if err != nil {
		return err
	}
	unix.Close(dbFD)
	dataFD, err = ensureServiceDirectory(storageFD, "storage")
	if err != nil {
		return err
	}
	logsFD, err := ensureServiceDirectory(dataFD, "logs")
	unix.Close(dataFD)
	if err != nil {
		return err
	}
	logFD, err := unix.Openat(logsFD, "sitebrush.log", unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	unix.Close(logsFD)
	if err != nil {
		return fmt.Errorf("prepare application journal: %w", err)
	}
	unix.Close(logFD)
	var rootStat unix.Stat_t
	if err := unix.Fstat(storageFD, &rootStat); err != nil {
		return fmt.Errorf("inspect storage root: %w", err)
	}
	if err := chownServiceStorageTree(storageFD, storagePath, &rootStat, uid, gid); err != nil {
		return fmt.Errorf("set service storage ownership: %w", err)
	}
	for _, components := range [][]string{{"storage", "letsencrypt"}, {"storage", "keys", "tls"}} {
		if err := restrictServiceSecretTree(storageFD, components); err != nil {
			return fmt.Errorf("protect certificate storage: %w", err)
		}
	}
	return nil
}

func restrictServiceSecretTree(storageFD int, components []string) error {
	directoryFD, err := unix.Dup(storageFD)
	if err != nil {
		return err
	}
	for _, component := range components {
		childFD, openErr := unix.Openat(directoryFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(directoryFD)
		if errors.Is(openErr, unix.ENOENT) {
			return nil
		}
		if openErr != nil {
			return openErr
		}
		directoryFD = childFD
	}
	defer unix.Close(directoryFD)
	return restrictServiceSecretDirectory(directoryFD)
}

func restrictServiceSecretDirectory(directoryFD int) error {
	if err := unix.Fchmod(directoryFD, 0o700); err != nil {
		return err
	}
	readFD, err := unix.Dup(directoryFD)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(readFD), "certificate storage")
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			err = restrictServiceSecretDirectory(childFD)
			unix.Close(childFD)
			if err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected certificate storage entry %s", name)
		}
		fileFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		err = unix.Fchmod(fileFD, 0o600)
		unix.Close(fileFD)
		if err != nil {
			return err
		}
	}
	return nil
}

// Parent directories stay owned by their existing owners. Newly created
// parents allow traversal while the data root remains private to the service.
func openServiceStoragePath(storagePath string, uid, gid int) (int, error) {
	parentFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	components := strings.Split(strings.TrimPrefix(filepath.Clean(storagePath), string(filepath.Separator)), string(filepath.Separator))
	for index, component := range components {
		mode := uint32(0o755)
		if index == len(components)-1 {
			mode = 0o700
		}
		childFD, openErr := unix.Openat(parentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) {
			created := unix.Mkdirat(parentFD, component, mode)
			if created != nil && !errors.Is(created, unix.EEXIST) {
				unix.Close(parentFD)
				return -1, fmt.Errorf("create storage component %s: %w", component, created)
			}
			childFD, openErr = unix.Openat(parentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr == nil && created == nil {
				openErr = unix.Fchmod(childFD, mode)
			}
		}
		unix.Close(parentFD)
		if openErr != nil {
			if childFD >= 0 {
				unix.Close(childFD)
			}
			return -1, fmt.Errorf("open storage component %s without following links: %w", component, openErr)
		}
		parentFD = childFD
		if index < len(components)-1 {
			var stat unix.Stat_t
			if err := unix.Fstat(parentFD, &stat); err != nil {
				unix.Close(parentFD)
				return -1, err
			}
			if !serviceCanTraverse(&stat, uid, gid) {
				unix.Close(parentFD)
				return -1, fmt.Errorf("storage parent %s must be traversable by the service user", component)
			}
		}
	}
	return parentFD, nil
}

func serviceCanTraverse(stat *unix.Stat_t, uid, gid int) bool {
	permissions := uint32(stat.Mode) & 0o777
	if permissions&0o001 != 0 {
		return true
	}
	return uint64(uid) == uint64(stat.Uid) && permissions&0o100 != 0 ||
		uint64(gid) == uint64(stat.Gid) && permissions&0o010 != 0
}

func ensureServiceDirectory(parentFD int, name string) (int, error) {
	if err := unix.Mkdirat(parentFD, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, fmt.Errorf("create storage directory %s: %w", name, err)
	}
	childFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open storage directory %s: %w", name, err)
	}
	return childFD, nil
}

func ensureServiceWorkingDirectory(storageFD int, relativePath string) error {
	parentFD, err := unix.Dup(storageFD)
	if err != nil {
		return err
	}
	for _, component := range strings.Split(relativePath, string(filepath.Separator)) {
		childFD, err := ensureServiceDirectory(parentFD, component)
		unix.Close(parentFD)
		if err != nil {
			return err
		}
		parentFD = childFD
	}
	return unix.Close(parentFD)
}

// Every ownership change uses an opened inode or a directory descriptor, so
// a worker replacing a path cannot redirect root's changes outside storage.
func chownServiceStorageTree(directoryFD int, path string, rootStat *unix.Stat_t, uid, gid int) error {
	var directoryStat unix.Stat_t
	if err := unix.Fstat(directoryFD, &directoryStat); err != nil {
		return err
	}
	if directoryStat.Dev != rootStat.Dev {
		return fmt.Errorf("storage crosses a filesystem mount at %s", path)
	}
	if err := unix.Fchown(directoryFD, uid, gid); err != nil {
		return err
	}
	if err := unix.Fchmod(directoryFD, 0o700); err != nil {
		return err
	}
	readFD, err := unix.Dup(directoryFD)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(readFD), path)
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		entryPath := filepath.Join(path, name)
		var stat unix.Stat_t
		if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("inspect %s: %w", entryPath, err)
		}
		if stat.Dev != rootStat.Dev {
			return fmt.Errorf("storage crosses a filesystem mount at %s", entryPath)
		}
		switch uint32(stat.Mode) & unix.S_IFMT {
		case unix.S_IFDIR:
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return fmt.Errorf("open %s: %w", entryPath, err)
			}
			err = chownServiceStorageTree(childFD, entryPath, rootStat, uid, gid)
			unix.Close(childFD)
			if err != nil {
				return err
			}
		case unix.S_IFREG:
			fileFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return fmt.Errorf("open %s: %w", entryPath, err)
			}
			var fileStat unix.Stat_t
			err = unix.Fstat(fileFD, &fileStat)
			if err == nil && (uint32(fileStat.Mode)&unix.S_IFMT != unix.S_IFREG || fileStat.Dev != rootStat.Dev || fileStat.Nlink != 1) {
				err = fmt.Errorf("storage file %s is not a single-link regular file", entryPath)
			}
			if err == nil {
				err = unix.Fchown(fileFD, uid, gid)
			}
			unix.Close(fileFD)
			if err != nil {
				return err
			}
		case unix.S_IFLNK:
			return fmt.Errorf("storage contains symbolic link %s", entryPath)
		default:
			// Transient sockets and pipes are recreated by their owning process.
		}
	}
	return nil
}
