package netchantls

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyNetChanWireCompatibility(t *testing.T) {
	if os.Getenv("SITEBRUSH_NETCHAN_INTEROP_TEST") != "1" {
		t.Skip("enable SITEBRUSH_NETCHAN_INTEROP_TEST=1 to build both released wire peers")
	}
	fixtureSource, err := os.ReadFile(filepath.Join("testdata", "interop.go"))
	if err != nil {
		t.Fatal(err)
	}
	var executables []string
	for _, fixture := range []struct{ module, version, compiler string }{
		{"github.com/matveynator/netchan", "v0.0.0-20260802175005-7848addf9431", "1.26.9"},
		{"github.com/matveynator/netchan/v2", "v2.1.0", "1.27.2"},
	} {
		directory := t.TempDir()
		source := strings.ReplaceAll(string(fixtureSource), "github.com/matveynator/netchan/v2", fixture.module)
		module := fmt.Sprintf("module github.com/matveynator/sitebrush/v2\n\ngo %s\n\nrequire %s %s\n", fixture.compiler, fixture.module, fixture.version)
		for name, contents := range map[string]string{"go.mod": module, "main.go": source} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		executable := filepath.Join(directory, "peer.exe")
		build := exec.Command("go", "build", "-mod=mod", "-o", executable, ".")
		build.Dir = directory
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=go"+fixture.compiler, "GOWORK=off")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", fixture.module, err, output)
		}
		executables = append(executables, executable)
	}
	for _, fixture := range []struct {
		name           string
		server, client int
	}{
		{"new-client-old-server", 0, 1}, {"old-client-new-server", 1, 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := exec.Command(executables[fixture.server], "listen")
			output, err := server.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			server.Stderr = &diagnostics
			if err := server.Start(); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- server.Wait() }()
			t.Cleanup(func() { _ = server.Process.Kill() })
			addresses := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(output)
				if scanner.Scan() {
					addresses <- scanner.Text()
				}
			}()
			var address string
			select {
			case address = <-addresses:
			case err := <-finished:
				t.Fatalf("listener ended: %v: %s", err, diagnostics.String())
			case <-time.After(10 * time.Second):
				t.Fatal("listener startup timed out")
			}
			client := exec.Command(executables[fixture.client], "dial", address)
			clientFinished := make(chan error, 1)
			var clientDiagnostics bytes.Buffer
			client.Stdout, client.Stderr = &clientDiagnostics, &clientDiagnostics
			if err := client.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Process.Kill() })
			go func() { clientFinished <- client.Wait() }()
			select {
			case err := <-clientFinished:
				if err != nil {
					t.Fatalf("client: %v: %s", err, clientDiagnostics.String())
				}
			case <-time.After(15 * time.Second):
				t.Fatal("cross-version delivery/close timed out")
			}
			select {
			case err := <-finished:
				if err != nil {
					t.Fatalf("server: %v: %s", err, diagnostics.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cross-version server shutdown timed out")
			}
		})
	}
}
