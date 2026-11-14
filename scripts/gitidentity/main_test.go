package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitRangeRejectsWrongNoreplyIDInEveryIdentity(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-q")
	createCommit := func(author, committer, message string) string {
		command := exec.Command("git", "-C", repository, "commit", "--allow-empty", "-m", message)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=SiteBrush owner", "GIT_AUTHOR_EMAIL="+author,
			"GIT_COMMITTER_NAME=SiteBrush owner", "GIT_COMMITTER_EMAIL="+committer,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("create commit: %v: %s", err, output)
		}
		return strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))
	}
	const ordinary = "owner@example.org"
	const wrong = "1970409+matveynator@users.noreply.github.com"
	base := createCommit(ordinary, ordinary, "base")
	createCommit(wrong, ordinary, "wrong author")
	createCommit(ordinary, wrong, "wrong committer")
	head := createCommit(ordinary, ordinary, "wrong trailer\n\nCo-authored-by: matveynator <"+wrong+">")
	count, err := checkCommitRange(repository, base, head, func(login string) (int64, error) {
		if login != "matveynator" {
			t.Fatalf("unexpected GitHub lookup for %q", login)
		}
		return 26712073, nil
	})
	if count != 3 || err == nil {
		t.Fatalf("checked=%d error=%v", count, err)
	}
	for _, role := range []string{"author", "committer", "Co-authored-by"} {
		if !strings.Contains(err.Error(), role+":") {
			t.Fatalf("missing %s failure: %v", role, err)
		}
	}
	count, err = checkCommitRange(repository, "", base, func(string) (int64, error) {
		t.Fatal("ordinary email triggered GitHub lookup")
		return 0, nil
	})
	if count != 1 || err != nil {
		t.Fatalf("single commit checked=%d error=%v", count, err)
	}
}

func TestNoreplyIdentityAcceptsMatchingIDAndOrdinaryEmail(t *testing.T) {
	lookups := 0
	lookup := func(login string) (int64, error) {
		lookups++
		if login != "matveynator" {
			t.Fatalf("unexpected lookup for %q", login)
		}
		return 26712073, nil
	}
	for _, email := range []string{
		"26712073+matveynator@users.noreply.github.com",
		"matvey@copters.ru",
		"matveynator@users.noreply.github.com",
	} {
		if err := checkNoreplyIdentity(commitIdentity{role: "author", email: email}, lookup); err != nil {
			t.Fatalf("valid email %q: %v", email, err)
		}
	}
	if lookups != 1 {
		t.Fatalf("GitHub lookups=%d, want one", lookups)
	}
	if err := checkNoreplyIdentity(commitIdentity{role: "author", email: "1970409+@users.noreply.github.com"}, lookup); err == nil {
		t.Fatal("empty GitHub login was accepted")
	}
}

func TestGitHubLookupChecksAccountAndCachesResult(t *testing.T) {
	requests := 0
	lookup := &githubLookup{
		apiURL: "https://api.github.test",
		token:  "test-token",
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if request.URL.String() != "https://api.github.test/users/matveynator" || request.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("unexpected GitHub request: %s", request.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"login":"matveynator","id":26712073}`))}, nil
		})},
		resolved: make(map[string]int64),
	}
	for range 2 {
		id, err := lookup.userID("matveynator")
		if err != nil || id != 26712073 {
			t.Fatalf("GitHub ID=%d err=%v", id, err)
		}
	}
	if requests != 1 {
		t.Fatalf("GitHub requests=%d, want one", requests)
	}
}

func TestCommitIdentityParsingRejectsMissingCommitter(t *testing.T) {
	_, err := parseCommitIdentities("tree 0123456789\nauthor Owner <owner@example.org> 1 +0000\n\nmessage")
	if err == nil {
		t.Fatal("missing committer was accepted")
	}
	if validRevision("-bad") || validRevision(strings.Repeat("g", 40)) || !validRevision(strings.Repeat("a", 40)) {
		t.Fatal("revision validation accepted an unsafe value or rejected a hash")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (transport roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func runGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", filepath.Clean(repository)}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
