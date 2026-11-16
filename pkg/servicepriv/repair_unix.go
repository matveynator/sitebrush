//go:build !windows

package servicepriv

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The launcher owns this session and its counters. No worker request can
// choose a repair path or request privileged filesystem operations.
type storageRepair struct {
	stop                               <-chan struct{}
	uid, gid                           int
	rootStat                           unix.Stat_t
	visited, repaired, skipped, failed int
}

func repairServiceStorage(storagePath, userID, groupID string, stop <-chan struct{}) {
	log.Printf("storage permission repair started path=%s", storagePath)
	uid, userErr := strconv.Atoi(userID)
	gid, groupErr := strconv.Atoi(groupID)
	if userErr != nil || groupErr != nil {
		log.Printf("storage permission repair failed: service identity does not fit native IDs")
		return
	}
	repair := storageRepair{stop: stop, uid: uid, gid: gid}
	err := repair.run(storagePath)
	log.Printf("storage permission repair finished path=%s visited=%d repaired=%d skipped=%d failed=%d error=%v", storagePath, repair.visited, repair.repaired, repair.skipped, repair.failed, err)
}

func (repair *storageRepair) run(storagePath string) error {
	if !filepath.IsAbs(storagePath) || filepath.Clean(storagePath) == "/" || repair.uid <= 0 || repair.gid < 0 {
		return errors.New("storage repair requires an absolute non-root path and an unprivileged owner")
	}
	if err := repair.checkSession(); err != nil {
		return err
	}
	// Resolve every component without following links, including the data root.
	directoryFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	for _, component := range strings.Split(strings.TrimPrefix(filepath.Clean(storagePath), "/"), "/") {
		childFD, openErr := unix.Openat(directoryFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(directoryFD)
		if openErr != nil {
			return fmt.Errorf("open storage component %s: %w", component, openErr)
		}
		directoryFD = childFD
	}
	defer unix.Close(directoryFD)
	if err := unix.Fstat(directoryFD, &repair.rootStat); err != nil {
		return err
	}
	return repair.directory(directoryFD, storagePath, 0)
}

func (repair *storageRepair) checkSession() error {
	select {
	case <-repair.stop:
		return errors.New("storage permission repair canceled")
	default:
		return nil
	}
}

func (repair *storageRepair) directory(directoryFD int, path string, depth int) error {
	if err := repair.checkSession(); err != nil {
		return err
	}
	// Bound descriptor use even for a maliciously deep service-writable tree.
	if depth >= 128 {
		return errors.New("storage repair directory depth limit reached")
	}
	if err := repair.inode(directoryFD, true); err != nil {
		return err
	}
	readFD, err := unix.Dup(directoryFD)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(readFD), path)
	defer directory.Close()
	for {
		if err := repair.checkSession(); err != nil {
			return err
		}
		entries, readErr := directory.ReadDir(64)
		for _, entry := range entries {
			if err := repair.checkSession(); err != nil {
				return err
			}
			entryPath := filepath.Join(path, entry.Name())
			if err := repair.entry(directoryFD, entry.Name(), entryPath, depth); err != nil {
				// Files can disappear during imports, rotation, or update cleanup.
				if !errors.Is(err, unix.ENOENT) {
					repair.failed++
					if repair.failed <= 20 {
						log.Printf("storage permission repair failed path=%s error=%v", entryPath, err)
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		// Backpressure limits metadata traffic while the HTTP worker serves requests.
		pause := time.NewTimer(2 * time.Millisecond)
		select {
		case <-repair.stop:
			pause.Stop()
			return repair.checkSession()
		case <-pause.C:
		}
	}
}

func (repair *storageRepair) entry(directoryFD int, name, path string, depth int) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	isDirectory := uint32(stat.Mode)&unix.S_IFMT == unix.S_IFDIR
	if stat.Dev != repair.rootStat.Dev || (!isDirectory && (uint32(stat.Mode)&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1)) {
		repair.skipped++
		return nil
	}
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if isDirectory {
		flags |= unix.O_DIRECTORY
	}
	entryFD, err := unix.Openat(directoryFD, name, flags, 0)
	if err != nil {
		return err
	}
	defer unix.Close(entryFD)
	if isDirectory {
		return repair.directory(entryFD, path, depth+1)
	}
	return repair.inode(entryFD, false)
}

// Validate the opened inode again: a concurrent worker may have replaced the
// directory entry since lstat. Changes never follow symbolic or hard links.
func (repair *storageRepair) inode(descriptor int, isDirectory bool) error {
	if err := repair.checkSession(); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		return err
	}
	wantedType := uint32(unix.S_IFREG)
	if isDirectory {
		wantedType = unix.S_IFDIR
	}
	if stat.Dev != repair.rootStat.Dev || uint32(stat.Mode)&unix.S_IFMT != wantedType || (!isDirectory && stat.Nlink != 1) {
		repair.skipped++
		return errors.New("opened storage inode is unsafe to repair")
	}
	repair.visited++
	mode := uint32(0o600)
	if isDirectory || uint32(stat.Mode)&0o100 != 0 {
		// Installed update binaries must remain executable by the service owner.
		mode = 0o700
	}
	ownerChanged := uint64(stat.Uid) != uint64(repair.uid) || uint64(stat.Gid) != uint64(repair.gid)
	if ownerChanged {
		if err := unix.Fchown(descriptor, repair.uid, repair.gid); err != nil {
			return err
		}
	}
	if ownerChanged || uint32(stat.Mode)&0o7777 != mode {
		if err := unix.Fchmod(descriptor, mode); err != nil {
			return err
		}
		repair.repaired++
	}
	return nil
}
