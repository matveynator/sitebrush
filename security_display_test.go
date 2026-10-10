package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/httpsecurity"
)

func TestSecurityBlockPaginationSearchesOutsideCurrentPage(t *testing.T) {
	now := time.Now().UTC()
	blocks := make([]analyticsSecurityBlockView, 51)
	for index := range blocks {
		blocks[index] = analyticsSecurityBlockView{SecurityBlock: httpsecurity.SecurityBlock{IP: fmt.Sprintf("192.0.2.%d", index+1), IncidentID: fmt.Sprintf("incident-%d", index), LastEvent: now.Add(time.Duration(index) * time.Second)}, Country: "RU", City: "Moscow", Agent: "Mozilla/5.0 (Windows NT 10.0)"}
	}
	request := httptest.NewRequest("GET", "/?analytics&tab=security&local_page=3&global_query=preserved", nil)
	visible, page := analyticsSecurityBlockPage(request, blocks, "local_page", "local_query", "local-blocks")
	if len(visible) != 1 || page.Total != 51 || page.Pages != 3 || page.Page != 3 || visible[0].IP != "192.0.2.1" {
		t.Fatalf("third page: %+v %+v", page, visible)
	}
	request = httptest.NewRequest("GET", "/?analytics&tab=security&local_page=3&local_query=incident-50", nil)
	visible, page = analyticsSecurityBlockPage(request, blocks, "local_page", "local_query", "local-blocks")
	if len(visible) != 1 || page.Page != 1 || page.Total != 1 || visible[0].OperatingSystem() != "Windows" {
		t.Fatalf("full-registry search: %+v %+v", page, visible)
	}
}

func TestRepeatedInterruptForcesExitDuringStalledShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix interrupt signals")
	}
	if os.Getenv("SITEBRUSH_TEST_REPEAT_INTERRUPT") == "1" {
		boundary, stop := signalAwareContext(context.Background())
		defer stop()
		fmt.Println("signal-ready")
		<-boundary.Done()
		fmt.Println("shutdown-started")
		select {}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRepeatedInterruptForcesExitDuringStalledShutdown$")
	child.Env = append(os.Environ(), "SITEBRUSH_TEST_REPEAT_INTERRUPT=1")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	lines := make(chan string, 4)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	awaitLine := func(expected string) {
		t.Helper()
		select {
		case received := <-lines:
			if received != expected {
				t.Fatalf("child: %q, want %q", received, expected)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("child did not reach %s", expected)
		}
	}
	awaitLine("signal-ready")
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	awaitLine("shutdown-started")
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- child.Wait() }()
	select {
	case err := <-finished:
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 130 {
			t.Fatalf("second interrupt did not force exit 130: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second interrupt left the child running")
	}
}
