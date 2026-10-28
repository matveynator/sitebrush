package sitefreeze

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Version paths are private, site-confined objects. Retaining before replacement
// copies only a changed file; taking a checkpoint only inventories references.
func ObjectPath(domain, digest string) string {
	site := sha256.Sum256([]byte(domain))
	return filepath.Join("storage", "file-versions", hex.EncodeToString(site[:]), digest)
}
func HashFile(root *os.Root, name string) (string, int64, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("file version must be a regular file")
	}
	hasher := sha256.New()
	count, err := io.Copy(hasher, file)
	return hex.EncodeToString(hasher.Sum(nil)), count, err
}
func Inventory(root *os.Root, directory, tree string, done <-chan struct{}) ([]File, error) {
	files := []File{}
	if _, err := root.Stat(directory); os.IsNotExist(err) {
		return files, nil
	} else if err != nil {
		return nil, err
	}
	err := fs.WalkDir(root.FS(), directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-done:
			return errors.New("file inventory canceled")
		default:
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		if strings.Contains(filepath.Base(relative), ".sitebrush-") {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, linkErr := root.Readlink(name)
			if linkErr != nil {
				return linkErr
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(relative), link))
			if filepath.IsAbs(link) || !filepath.IsLocal(resolved) {
				return errors.New("file link escapes the site tree")
			}
			if _, linkErr = root.Stat(filepath.Join(directory, resolved)); linkErr != nil {
				return linkErr
			}
			digest := sha256.Sum256([]byte("symlink\x00" + link))
			files = append(files, File{Tree: tree, Path: filepath.ToSlash(relative), Hash: hex.EncodeToString(digest[:]), Link: link})
			return nil
		}
		digest, count, err := HashFile(root, name)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, File{Tree: tree, Path: filepath.ToSlash(relative), Hash: digest, Bytes: count, Mode: uint32(info.Mode().Perm())})
		return nil
	})
	return files, err
}
func Retain(root *os.Root, domain, name string) error {
	if info, err := root.Lstat(name); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	digest, count, err := HashFile(root, name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return RetainKnown(root, domain, name, digest, count)
}

// Inventory already hashed the files under the caller's exclusive site lease.
// Reusing those digests avoids a second full read of every large asset.
func RetainKnown(root *os.Root, domain, name, digest string, count int64) error {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) || count < 0 {
		return errors.New("invalid retained file digest")
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != count {
		return errors.New("file changed while retaining its version")
	}
	object := ObjectPath(domain, digest)
	if info, statErr := root.Stat(object); statErr == nil {
		if info.Size() != count {
			return errors.New("file version length mismatch")
		}
		return recordOrigin(root, object, name)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err = root.MkdirAll(filepath.Dir(object), 0700); err != nil {
		return err
	}
	if err = root.Link(name, object); err == nil {
		actual, _, verifyErr := HashFile(root, object)
		if verifyErr == nil && actual == digest {
			if err = syncParent(root, object); err != nil {
				return err
			}
			return recordOrigin(root, object, name)
		}
		root.Remove(object)
		if verifyErr != nil {
			return verifyErr
		}
		return errors.New("file changed while retaining its version")
	}
	source, err := root.Open(name)
	if err != nil {
		return err
	}
	defer source.Close()
	if err = atomicCopy(root, source, object, 0600, digest); err != nil {
		return err
	}
	return recordOrigin(root, object, name)
}
func Restore(root *os.Root, domain, target string, version File) error {
	if version.Link != "" {
		if filepath.IsAbs(version.Link) || !filepath.IsLocal(filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(version.Path)), version.Link))) {
			return errors.New("file link escapes the site tree")
		}
		if err := root.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		temporary := target + ".sitebrush-link-" + randomName()
		defer root.Remove(temporary)
		if err := root.Symlink(version.Link, temporary); err != nil {
			return err
		}
		if err := root.Rename(temporary, target); err != nil {
			return err
		}
		return syncParent(root, target)
	}
	decoded, decodeErr := hex.DecodeString(version.Hash)
	if !filepath.IsLocal(filepath.FromSlash(version.Path)) || decodeErr != nil || len(decoded) != 32 || version.Hash != strings.ToLower(version.Hash) {
		return errors.New("invalid file version")
	}
	objectName := ObjectPath(domain, version.Hash)
	if digest, _, err := HashFile(root, target); err == nil && digest == version.Hash {
		if info, statErr := root.Stat(target); statErr == nil && info.Mode().Perm() == os.FileMode(version.Mode) {
			return recordOrigin(root, objectName, target)
		}
	}
	if info, statErr := root.Stat(objectName); statErr == nil && info.Mode().Perm() == os.FileMode(version.Mode) {
		if err := root.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		temporary := target + ".sitebrush-restore-" + randomName()
		defer root.Remove(temporary)
		if linkErr := root.Link(objectName, temporary); linkErr == nil {
			actual, _, verifyErr := HashFile(root, temporary)
			if verifyErr != nil {
				return verifyErr
			}
			if actual != version.Hash {
				return errors.New("corrupt retained file version")
			}
			if err := root.Rename(temporary, target); err != nil {
				return err
			}
			if err := syncParent(root, target); err != nil {
				return err
			}
			return recordOrigin(root, objectName, target)
		}
	}
	object, err := root.Open(ObjectPath(domain, version.Hash))
	if err != nil {
		return err
	}
	defer object.Close()
	if err = atomicCopy(root, object, target, os.FileMode(version.Mode), version.Hash); err != nil {
		return err
	}
	return recordOrigin(root, objectName, target)
}
func atomicCopy(root *os.Root, source io.Reader, target string, mode os.FileMode, expected string) error {
	if err := root.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	temporary := target + ".sitebrush-version-" + randomName()
	defer root.Remove(temporary)
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	_, err = io.Copy(io.MultiWriter(file, hasher), source)
	if err == nil && hex.EncodeToString(hasher.Sum(nil)) != expected {
		err = errors.New("file changed while retaining its version")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(temporary, target); err != nil {
		return err
	}
	return syncParent(root, target)
}

// A durable SQL checkpoint must never get ahead of its filesystem directory entries.
func syncParent(root *os.Root, target string) error {
	directory, err := root.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer directory.Close()
	// Windows does not expose directory fsync through os.File.Sync.
	if runtime.GOOS != "windows" {
		if err = directory.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
			return err
		}
	}
	return nil
}
func randomName() string { return rand.Text() }

type origin struct {
	Path   string
	Linked bool
}

func recordOrigin(root *os.Root, object, name string) error {
	first, err := root.Stat(object)
	if err != nil {
		return err
	}
	second, err := root.Stat(name)
	if err != nil {
		return err
	}
	directory := object + ".origins"
	if err = root.MkdirAll(directory, 0700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(name))
	encoded, err := json.Marshal(origin{Path: name, Linked: os.SameFile(first, second)})
	if err != nil {
		return err
	}
	originPath := filepath.Join(directory, hex.EncodeToString(digest[:]))
	if existing, openErr := root.Open(originPath); openErr == nil {
		previous, readErr := io.ReadAll(io.LimitReader(existing, 1024))
		closeErr := existing.Close()
		if readErr == nil && closeErr == nil && string(previous) == string(encoded) {
			return nil
		}
	} else if !os.IsNotExist(openErr) {
		return openErr
	}
	payloadHash := sha256.Sum256(encoded)
	return atomicCopy(root, strings.NewReader(string(encoded)), originPath, 0600, hex.EncodeToString(payloadHash[:]))
}

// ExtraBytes counts retained content once. Links to a current file consume no
// additional content budget; copied versions use a hash check only as fallback.
func ExtraBytes(root *os.Root, domain string) (int64, error) {
	directory := filepath.Dir(ObjectPath(domain, strings.Repeat("0", 64)))
	folder, err := root.Open(directory)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer folder.Close()
	var retained int64
	for {
		objects, readErr := folder.ReadDir(128)
		for _, entry := range objects {
			if entry.IsDir() || len(entry.Name()) != 64 {
				continue
			}
			info, statErr := entry.Info()
			if statErr != nil {
				return 0, statErr
			}
			object := filepath.Join(directory, entry.Name())
			live := false
			references, openErr := root.Open(object + ".origins")
			if openErr == nil {
				for {
					batch, batchErr := references.ReadDir(64)
					for _, reference := range batch {
						encoded, refErr := root.ReadFile(filepath.Join(object+".origins", reference.Name()))
						if refErr != nil {
							references.Close()
							return 0, refErr
						}
						var contact origin
						if json.Unmarshal(encoded, &contact) != nil || !filepath.IsLocal(contact.Path) {
							references.Close()
							return 0, errors.New("invalid version origin")
						}
						current, currentErr := root.Stat(contact.Path)
						if currentErr == nil {
							if os.SameFile(info, current) {
								live = true
								break
							}
							if !contact.Linked {
								hash, _, hashErr := HashFile(root, contact.Path)
								if hashErr != nil {
									references.Close()
									return 0, hashErr
								}
								if hash == entry.Name() {
									live = true
									break
								}
							}
						} else if !os.IsNotExist(currentErr) {
							references.Close()
							return 0, currentErr
						}
					}
					if live || batchErr == io.EOF {
						break
					}
					if batchErr != nil {
						references.Close()
						return 0, batchErr
					}
				}
				references.Close()
			} else if !os.IsNotExist(openErr) {
				return 0, openErr
			}
			if !live {
				retained += info.Size()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	return retained, nil
}
