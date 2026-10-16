package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testRelease(t *testing.T, payload []byte) (Manifest, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return Manifest{Protocol: Protocol, Version: "2", Sequence: 2, Commit: strings.Repeat("a", 40), Published: time.Now().UTC(), Compatibility: Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}, Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Name: BinaryName(runtime.GOOS, runtime.GOARCH), Size: int64(len(payload)), SHA256: hex.EncodeToString(digest[:])}}}, publicKey, privateKey
}

func TestReleaseTrustAndPlatform(t *testing.T) {
	manifest, publicKey, privateKey := testRelease(t, []byte("server"))
	signed, err := Sign(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(signed, publicKey)
	if err != nil || verified.Version != "2" {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
	var envelope SignedManifest
	if err := json.Unmarshal(signed, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Manifest = bytes.Replace(envelope.Manifest, []byte(`"version":"2"`), []byte(`"version":"3"`), 1)
	tampered, _ := json.Marshal(envelope)
	if _, err := Verify(tampered, publicKey); err == nil {
		t.Fatal("accepted a modified manifest")
	}
	stranger, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(signed, stranger); err == nil {
		t.Fatal("accepted an unknown signing key")
	}
	if _, err := Verify(append(signed, []byte("{}")...), publicKey); err == nil {
		t.Fatal("accepted trailing content")
	}
	if _, err := Verify(bytes.Repeat([]byte("x"), manifestLimit+1), publicKey); err == nil {
		t.Fatal("accepted an oversized manifest")
	}
	if _, err := manifest.Artifact("plan9", "mips"); err == nil {
		t.Fatal("accepted an unavailable target")
	}
	for _, change := range []func(*Manifest){func(m *Manifest) { m.Sequence = 1 }, func(m *Manifest) { m.Protocol++ }, func(m *Manifest) { m.Protocol-- }, func(m *Manifest) { m.Compatibility = Compatibility{} }, func(m *Manifest) { m.Artifacts[0].Name = "../server" }, func(m *Manifest) { m.Artifacts[0].SHA256 = "bad" }} {
		candidate := manifest
		candidate.Artifacts = append([]Artifact(nil), manifest.Artifacts...)
		change(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("accepted invalid manifest %+v", candidate)
		}
	}
}

func TestDownloadPreservesInstallationOnFailures(t *testing.T) {
	for _, scenario := range []string{"http", "truncated", "checksum", "too-large", "success"} {
		t.Run(scenario, func(t *testing.T) {
			expected := []byte("new verified server")
			manifest, publicKey, _ := testRelease(t, expected)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "http":
					w.WriteHeader(502)
				case "truncated":
					_, _ = w.Write(expected[:3])
				case "checksum":
					_, _ = w.Write(bytes.Repeat([]byte("x"), len(expected)))
				case "too-large":
					_, _ = w.Write(append(expected, 'x'))
				default:
					_, _ = w.Write(expected)
				}
			}))
			defer server.Close()
			directory := t.TempDir()
			current := filepath.Join(directory, "working")
			if err := os.WriteFile(current, []byte("old server"), 0o700); err != nil {
				t.Fatal(err)
			}
			catalog := NewCatalog(publicKey)
			catalog.client = server.Client()
			catalog.artifactBase = server.URL + "/"
			destination := filepath.Join(directory, "candidate")
			err := catalog.Download(manifest, destination, make(chan struct{}))
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("expected download rejection")
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("partial release was retained")
				}
			}
			contents, err := os.ReadFile(current)
			if err != nil || string(contents) != "old server" {
				t.Fatal("working installation was modified")
			}
		})
	}
}

func TestOfficialReleaseURLAllowlist(t *testing.T) {
	for _, address := range []string{"http://github.com/" + Repository + "/releases/download/stable-release/update.json", "https://github.com/attacker/repo/releases/download/server", "https://github.com.evil.test/" + Repository + "/releases/download/server", "https://github.com:444/" + Repository + "/releases/download/server", "https://user@github.com/" + Repository + "/releases/download/server"} {
		request, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			t.Fatal(err)
		}
		if officialURL(request.URL) {
			t.Fatalf("trusted %s", address)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, "https://github.com/"+Repository+"/releases/download/release-2/sitebrush_linux_amd64", nil)
	if !officialURL(request.URL) {
		t.Fatal("official artifact rejected")
	}
}

func TestBackupRecoveryAndCorruption(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "storage", "db", "sites"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "storage", "db", "sites", "site.db"), []byte("database with committed WAL content"), 0o640); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup, err := snapshot(directory, store, "operation", make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "storage", "db", "sites", "site.db"), []byte("migrated"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "new-format"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restore(directory, store, backup); err != nil {
		t.Fatal(err)
	}
	if err := restore(directory, store, backup); err != nil {
		t.Fatalf("repeated recovery: %v", err)
	}
	contents, _ := os.ReadFile(filepath.Join(directory, "storage", "db", "sites", "site.db"))
	if string(contents) != "database with committed WAL content" {
		t.Fatal("database was not restored")
	}
	if contents, err := os.ReadFile(filepath.Join(directory, "new-format")); err != nil || string(contents) != "new" {
		t.Fatal("database recovery changed a non-database file", err)
	}
	if err := store.WriteFile(filepath.Join(backup, "files", "storage", "db", "sites", "site.db"), []byte("corrupted"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := restore(directory, store, backup); err == nil {
		t.Fatal("accepted corrupt recovery snapshot")
	}
	contents, _ = os.ReadFile(filepath.Join(directory, "storage", "db", "sites", "site.db"))
	if string(contents) != "database with committed WAL content" {
		t.Fatal("corrupt backup changed working data")
	}
}

func TestBackupRefusesSymlinksAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "storage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "storage", "db")); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := snapshot(root, store, "symlink", make(chan struct{})); err == nil {
		t.Fatal("backup followed a symbolic link")
	}
	if err := os.Remove(filepath.Join(root, "storage", "db")); err != nil {
		t.Fatal(err)
	}
	canceled := make(chan struct{})
	close(canceled)
	if _, err := snapshot(root, store, "canceled", canceled); err == nil {
		t.Fatal("backup ignored cancellation")
	}
}

func TestRollbackCompatibility(t *testing.T) {
	old := Version{Compatibility: Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}}
	compatible := Version{Compatibility: old.Compatibility}
	incompatible := Version{Compatibility: Compatibility{Generation: 2, ReadsFrom: 1, ReadsThrough: 2, WritesCompatible: false}}
	if !canRollback(old, compatible) || canRollback(old, incompatible) {
		t.Fatal("unsafe rollback policy")
	}
	incompatible.Compatibility.WritesCompatible = true
	if canRollback(old, incompatible) {
		t.Fatal("old binary cannot read the new data generation")
	}
}

func TestCanceledCatalogRequest(t *testing.T) {
	manifest, publicKey, privateKey := testRelease(t, []byte("server"))
	payload, err := Sign(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, bytes.NewReader(payload)) }))
	defer server.Close()
	catalog := NewCatalog(publicKey)
	catalog.client = server.Client()
	catalog.manifestURL = server.URL
	stop := make(chan struct{})
	close(stop)
	if _, _, err := catalog.Check(stop); err == nil {
		t.Fatal("catalog ignored canceled consumer")
	}
}

func TestRevokedChannelContactCancelsWithoutPanic(t *testing.T) {
	replies := make(chan Response)
	close(replies)
	if err := sendChannel(replies, Response{}, nil, time.Second); err == nil {
		t.Fatal("closed contact accepted a response")
	}
	stalled := make(chan Response)
	if err := sendChannel(stalled, Response{}, nil, time.Millisecond); err == nil {
		t.Fatal("stalled subscriber was not terminated")
	}
	stopped := make(chan struct{})
	close(stopped)
	if err := sendChannel(make(chan Response, 1), Response{}, stopped, time.Second); err == nil {
		t.Fatal("canceled consumer received work")
	}
}

func TestPublishRequiresCompleteSignedServerMatrix(t *testing.T) {
	root := t.TempDir()
	_, publicKey, privateKey := testRelease(t, []byte("server"))
	for _, goos := range []string{"darwin", "linux", "windows", "freebsd", "openbsd", "netbsd"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			if err := os.WriteFile(filepath.Join(root, BinaryName(goos, goarch)), []byte(goos+goarch), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "untrusted.zip"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}
	if err := publish(root, "2", strings.Repeat("a", 40), privateKey, publicKey, policy); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "update.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(payload, publicKey)
	if err != nil || len(manifest.Artifacts) != 12 {
		t.Fatalf("published matrix=%+v err=%v", manifest, err)
	}
	if err := os.Remove(filepath.Join(root, "update.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, BinaryName("windows", "arm64"))); err != nil {
		t.Fatal(err)
	}
	if err := publish(root, "3", strings.Repeat("b", 40), privateKey, publicKey, policy); err == nil {
		t.Fatal("published partial matrix")
	}
	if _, err := os.Stat(filepath.Join(root, "update.json")); !os.IsNotExist(err) {
		t.Fatal("failed publish left an installable manifest")
	}
	stranger, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := publish(root, "3", strings.Repeat("b", 40), privateKey, stranger, policy); err == nil {
		t.Fatal("accepted untrusted publisher")
	}
}

func TestBackupRejectsTruncatedInventory(t *testing.T) {
	root := t.TempDir()
	writeBackupFixtureFile(t, root, "storage/db/sitebrush.db", []byte("content"))
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup, err := snapshot(root, store, "inventory", make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFile(filepath.Join(backup, "index.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restore(root, store, backup); err == nil {
		t.Fatal("truncated inventory was accepted")
	}
}

func TestServerSessionGuardsRecoveryUntilProcessReleasesDatabases(t *testing.T) {
	root := newFixtureInstallation(t)
	first, err := ServerSession(root, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ServerSession(root, nil, 0); err == nil {
		second.Close()
		t.Fatal("second database owner acquired a live process session")
	}
	stop := make(chan struct{})
	close(stop)
	if _, err := ServerSession(root, stop, time.Second); err == nil {
		t.Fatal("recovery ignored cancellation while database session was live")
	}
	first.Close()
	recovered, err := ServerSession(root, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	recovered.Close()
}

func TestPartitionedStorageContractRejectsPredecessorWrites(t *testing.T) {
	predecessor := Version{Compatibility: Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}}
	successor := Version{Compatibility: CurrentCompatibility()}
	if successor.Compatibility.MigrationMinimum() > predecessor.Compatibility.Generation {
		t.Fatal("legacy sites cannot migrate")
	}
	if canRollback(predecessor, successor) {
		t.Fatal("predecessor could reopen partitioned security storage after activation")
	}
}
