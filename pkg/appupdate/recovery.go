package appupdate

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

func sessionStopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// Recovery is independent of the canceled operation. Service shutdown leaves
// its durable boundary intact for the next exclusive installation session.
func recoverOperationData(options Options, store *os.Root, state *State, stop <-chan struct{}) error {
	if sessionStopped(stop) {
		return errors.New("service stopping; recovery will continue on next startup")
	}
	if state.RecoveryVersion == nil {
		return errors.New("missing previous version for recovery")
	}
	old := *state.RecoveryVersion
	if state.DataChanged {
		if state.Backup != "" {
			if err := restore(options.StoragePath, store, state.Backup); err != nil {
				return err
			}
		}
		policy := FullBackupPolicy()
		if state.BackupPolicy != nil {
			policy = *state.BackupPolicy
		}
		if policy != FullBackupPolicy() && (state.Candidate == nil || !canRollback(old, *state.Candidate)) {
			return errors.New("unbacked data may be incompatible; restore an external backup or repair the candidate before restarting")
		}
		if digest, err := fileDigest(store, old.Binary); err != nil || digest != old.SHA256 {
			return errors.New("recovery binary checksum mismatch")
		}
		arguments := append(append([]string{}, state.Arguments...), "-update-preflight", "-update-target-version", old.Version)
		directory := state.WorkingDirectory
		if directory == "" {
			directory = options.WorkingDirectory
		}
		if err := runCandidate(filepath.Join(options.StoragePath, Directory, old.Binary), arguments, directory, options.Output, 2*time.Minute, stop); err != nil {
			return err
		}
	}
	state.Current = old
	state.Candidate = nil
	state.DataChanged = false
	state.RecoveryRequired = false
	return nil
}
