package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	browserstats "github.com/matveynator/sitebrush/v2/pkg/analytics"
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
	for _, link := range page.Links {
		target, err := url.Parse(link.URL)
		if err != nil || target.Fragment != "local-blocks" || target.Query().Get("global_query") != "preserved" {
			t.Fatalf("pagination lost list anchor or filters: %+v", link)
		}
	}
	incidentRequest := httptest.NewRequest("GET", "/?analytics&tab=security&incident_page=2", nil)
	incidentPage, _, _ := analyticsSecurityPage(incidentRequest, "incident_page", "", "security-incidents", 80)
	for _, link := range incidentPage.Links {
		target, err := url.Parse(link.URL)
		if err != nil || target.Fragment != "security-incidents" {
			t.Fatalf("incident page starts at top: %+v", link)
		}
	}
	request = httptest.NewRequest("GET", "/?analytics&tab=security&local_page=3&local_query=incident-50", nil)
	visible, page = analyticsSecurityBlockPage(request, blocks, "local_page", "local_query", "local-blocks")
	if len(visible) != 1 || page.Page != 1 || page.Total != 1 || visible[0].OperatingSystem() != "Windows" {
		t.Fatalf("full-registry search: %+v %+v", page, visible)
	}
}

func TestBlockedIPStatisticsCountAddressesAndRespectPolicyPrecedence(t *testing.T) {
	blocks := []analyticsBlockedIPMapPoint{
		{Address: "192.0.2.1", Country: "RU", Policy: "local", GeoKnown: true, AttackTypes: []string{"repository", "repository", "secret"}},
		{Address: "192.0.2.1", Country: "RU", Policy: "local", AttackTypes: []string{"repository"}},
		{Address: "192.0.2.2", Country: "DE", Policy: "global", GeoKnown: true, AttackTypes: []string{"repository"}},
		{Address: "192.0.2.3", Policy: "manual", AttackTypes: []string{"unknown"}},
		{Address: "192.0.2.4", Policy: "global", AttackTypes: []string{"repository"}},
	}
	allowlist := []httpsecurity.SecurityAllow{{IP: "192.0.2.4"}, {IP: "192.0.2.4"}}
	throttles := []httpsecurity.SecurityThrottle{{IP: "192.0.2.1"}, {IP: "192.0.2.4"}, {IP: "192.0.2.5"}}
	report := browserstats.SecurityReport{ReturnTrackingStarted: time.Now(), BlockedReturns: []browserstats.BlockedReturn{{IP: "192.0.2.1", Requests: 50, Visits: 1}, {IP: "192.0.2.2", Requests: 2, Visits: 2}}}
	summary := analyticsSecurityIPStatistics(blocks, allowlist, throttles, report)
	if summary.Blocked != 3 || summary.Allowed != 1 || summary.Throttled != 1 || summary.ReturningIPs != 2 || summary.ReturnRequests != 52 || summary.ReturnVisits != 3 || summary.UnknownLocations != 1 {
		t.Fatalf("mixed units or policy overlaps: %+v", summary)
	}
	for _, row := range summary.Attacks {
		if row.Label == "repository" && row.Count != 2 {
			t.Fatalf("category counted twice per IP: %+v", row)
		}
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
