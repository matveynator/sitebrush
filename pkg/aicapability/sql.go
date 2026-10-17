package aicapability

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SQL documents contain credential hashes and grant metadata only. Their
// transaction belongs to the control database participant invoking these helpers.
func LoadSQL(database *sql.DB) ([]byte, []byte, error) {
	var capabilities, oauth []byte
	err := database.QueryRow(`SELECT capabilities,oauth FROM ai_security_state WHERE id=1`).Scan(&capabilities, &oauth)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	return capabilities, oauth, err
}

func InitSQL(database *sql.DB) error {
	_, err := database.Exec(`CREATE TABLE IF NOT EXISTS ai_security_state(id INTEGER PRIMARY KEY,capabilities TEXT NOT NULL,oauth TEXT NOT NULL)`)
	return err
}

func SaveSQL(database *sql.DB, capabilities, oauth []byte) error {
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.Exec(`DELETE FROM ai_security_state WHERE id=1`); err != nil {
		return err
	}
	if _, err = transaction.Exec(`INSERT INTO ai_security_state(id,capabilities,oauth) VALUES(1,?,?)`, string(capabilities), string(oauth)); err != nil {
		return err
	}
	return transaction.Commit()
}

type storageRequest struct {
	save                bool
	capabilities, oauth []byte
	reply               chan storageResponse
}
type storageResponse struct {
	capabilities, oauth []byte
	err                 error
}
type fileStorage struct {
	requests chan storageRequest
	stop     chan chan struct{}
}

// NewPersistentManager is retained for standalone users. The application uses
// NewStoredManager with its site control participant instead of a separate store.
func NewPersistentManager(path string) *Manager {
	if path == "" {
		return NewManager()
	}
	storage := &fileStorage{requests: make(chan storageRequest), stop: make(chan chan struct{})}
	go storage.run(path)
	return NewStoredManager(storage)
}

func (storage *fileStorage) run(path string) {
	var database *sql.DB
	err := os.MkdirAll(filepath.Dir(path), 0700)
	if err == nil {
		database, err = sql.Open("sqlite", "file:"+path+".db")
	}
	if database != nil {
		defer database.Close()
		database.SetMaxOpenConns(1)
		if err == nil {
			_, err = database.Exec(`PRAGMA synchronous=FULL`)
		}
		if err == nil {
			err = InitSQL(database)
		}
		if err == nil {
			err = os.Chmod(path+".db", 0600)
		}
		if err == nil {
			err = migrateStandaloneSQL(database, path)
		}
	}
	for {
		select {
		case request := <-storage.requests:
			response := storageResponse{err: err}
			if err == nil {
				if request.save {
					response.err = SaveSQL(database, request.capabilities, request.oauth)
				} else {
					response.capabilities, response.oauth, response.err = LoadSQL(database)
				}
			}
			request.reply <- response
		case reply := <-storage.stop:
			close(reply)
			return
		}
	}
}

func migrateStandaloneSQL(database *sql.DB, path string) error {
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS ai_file_migration(id INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var completed int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ai_file_migration WHERE id=1`).Scan(&completed); err != nil {
		return err
	}
	cleanup := func() error {
		for _, source := range []string{path, path + ".oauth"} {
			if err := os.Remove(source); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if completed != 0 {
		return cleanup()
	}
	read := func(source string) ([]byte, error) {
		file, err := os.Open(source)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer file.Close()
		encoded, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
		if len(encoded) > 32<<20 {
			return nil, errors.New("legacy AI storage exceeds migration budget")
		}
		return encoded, err
	}
	capabilities, err := read(path)
	if err != nil {
		return err
	}
	oauth, err := read(path + ".oauth")
	if err != nil {
		return err
	}
	if len(capabilities) == 0 && len(oauth) == 0 {
		return nil
	}
	sites, err := SplitLegacy(capabilities, oauth)
	if err != nil {
		return err
	}
	combinedCapabilities := map[string]Capability{}
	combinedOAuth := &oauthState{Clients: map[string]OAuthClient{}, Refresh: map[string]oauthRefresh{}}
	for _, documents := range sites {
		siteCapabilities := map[string]Capability{}
		siteOAuth := &oauthState{}
		if err := json.Unmarshal(documents[0], &siteCapabilities); err != nil {
			return err
		}
		if err := json.Unmarshal(documents[1], siteOAuth); err != nil {
			return err
		}
		for key, grant := range siteCapabilities {
			combinedCapabilities[key] = grant
		}
		for key, client := range siteOAuth.Clients {
			combinedOAuth.Clients[key] = client
		}
		for key, refresh := range siteOAuth.Refresh {
			combinedOAuth.Refresh[key] = refresh
		}
	}
	capabilities, err = json.Marshal(combinedCapabilities)
	if err != nil {
		return err
	}
	oauth, err = json.Marshal(combinedOAuth)
	if err != nil {
		return err
	}
	previous, _, err := LoadSQL(database)
	if err != nil {
		return err
	}
	if len(previous) > 0 {
		return errors.New("legacy AI storage conflicts with control database")
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`INSERT INTO ai_security_state(id,capabilities,oauth) VALUES(1,?,?)`, string(capabilities), string(oauth)); err != nil {
		return err
	}
	var checkCapabilities, checkOAuth []byte
	if err := transaction.QueryRow(`SELECT capabilities,oauth FROM ai_security_state WHERE id=1`).Scan(&checkCapabilities, &checkOAuth); err != nil {
		return err
	}
	if !bytes.Equal(checkCapabilities, capabilities) || !bytes.Equal(checkOAuth, oauth) {
		return errors.New("legacy AI import verification failed")
	}
	if _, err := transaction.Exec(`INSERT INTO ai_file_migration(id) VALUES(1)`); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	return cleanup()
}

func (storage *fileStorage) Load() ([]byte, []byte, error) {
	reply := make(chan storageResponse, 1)
	storage.requests <- storageRequest{reply: reply}
	response := <-reply
	return response.capabilities, response.oauth, response.err
}
func (storage *fileStorage) Save(capabilities, oauth []byte) error {
	reply := make(chan storageResponse, 1)
	storage.requests <- storageRequest{save: true, capabilities: capabilities, oauth: oauth, reply: reply}
	return (<-reply).err
}
func (storage *fileStorage) Close() { reply := make(chan struct{}); storage.stop <- reply; <-reply }
