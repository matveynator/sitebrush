//go:build !windows

package serviceinstall

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
	if err := validateServiceStoragePath(installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath())); err != nil {
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

func validateServiceStoragePath(storagePath string) error {
	if !filepath.IsAbs(storagePath) || filepath.Clean(storagePath) == string(filepath.Separator) {
		return fmt.Errorf("storage folder must be an absolute non-root path")
	}
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(filepath.Clean(storagePath), current), current) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect storage path %s: %w", current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("storage path component %s is not a real directory", current)
		}
	}
	if info, err := os.Stat(storagePath); err == nil && info.IsDir() {
		return filepath.WalkDir(storagePath, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("storage contains symbolic link %s", path)
			}
			return nil
		})
	}
	return nil
}

func prepareServiceStorage(plan installPlan) error {
	storagePath := installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath())
	if err := validateServiceStoragePath(storagePath); err != nil {
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
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	if err := createServiceStoragePath(storagePath, uid, gid); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(storagePath, "storage", "db"), 0o700); err != nil {
		return fmt.Errorf("create storage directories: %w", err)
	}
	// Validate the entire tree before changing any ownership.
	check := func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("storage contains symbolic link %s", path)
		}
		return nil
	}
	if err := filepath.WalkDir(storagePath, check); err != nil {
		return err
	}
	if err := filepath.WalkDir(storagePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("set service storage ownership: %w", err)
	}
	return nil
}

// Parent directories stay owned by their existing owners. Newly created
// parents allow traversal while the data root remains private to the service.
func createServiceStoragePath(storagePath string, uid, gid int) error {
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(filepath.Clean(storagePath), current), current)
	for index, component := range components {
		current = filepath.Join(current, component)
		mode := os.FileMode(0o755)
		if index == len(components)-1 {
			mode = 0o700
		}
		if err := os.Mkdir(current, mode); err == nil {
			if err := os.Chmod(current, mode); err != nil {
				return fmt.Errorf("set storage path permissions %s: %w", current, err)
			}
		} else if !os.IsExist(err) {
			return fmt.Errorf("create storage path %s: %w", current, err)
		}
		if index < len(components)-1 {
			info, err := os.Stat(current)
			if err != nil {
				return err
			}
			if !info.IsDir() || !serviceCanTraverse(info, uid, gid) {
				return fmt.Errorf("storage parent %s must be traversable by the service user", current)
			}
		}
	}
	return nil
}

func serviceCanTraverse(info os.FileInfo, uid, gid int) bool {
	permissions := info.Mode().Perm()
	if permissions&0o001 != 0 {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint32(uid) == stat.Uid && permissions&0o100 != 0 ||
		uint32(gid) == stat.Gid && permissions&0o010 != 0
}
