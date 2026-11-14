// gitidentity rejects GitHub noreply addresses whose numeric ID belongs to
// someone other than the login written in the same address.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type commitIdentity struct {
	role  string
	email string
}

type githubLookup struct {
	client   *http.Client
	apiURL   string
	token    string
	resolved map[string]int64
}

func main() {
	base := flag.String("base", "", "commit before the changes; empty checks only head")
	head := flag.String("head", "HEAD", "last commit to check")
	flag.Parse()
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	apiURL := os.Getenv("GITHUB_API_URL")
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	lookup := &githubLookup{
		client:   &http.Client{Timeout: 10 * time.Second},
		apiURL:   strings.TrimRight(apiURL, "/"),
		token:    token,
		resolved: make(map[string]int64),
	}
	count, err := checkCommitRange(".", *base, *head, lookup.userID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("checked GitHub noreply attribution in %d commits\n", count)
}

func checkCommitRange(repository, base, head string, userID func(string) (int64, error)) (int, error) {
	commits, err := commitIDs(repository, base, head)
	if err != nil {
		return 0, err
	}
	var failures []error
	for _, commit := range commits {
		object, err := exec.Command("git", "-C", repository, "cat-file", "commit", commit).Output()
		if err != nil {
			return 0, fmt.Errorf("read commit %s: %w", commit, err)
		}
		identities, err := parseCommitIdentities(string(object))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", commit, err))
			continue
		}
		for _, identity := range identities {
			if err := checkNoreplyIdentity(identity, userID); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", commit, err))
			}
		}
	}
	return len(commits), errors.Join(failures...)
}

func commitIDs(repository, base, head string) ([]string, error) {
	if !validRevision(head) || base != "" && !validRevision(base) {
		return nil, fmt.Errorf("commit revisions must be full hashes or HEAD")
	}
	if strings.Trim(base, "0") == "" {
		base = ""
	}
	arguments := []string{"-C", repository, "rev-list"}
	if base == "" {
		arguments = append(arguments, "--max-count=1", head)
	} else {
		arguments = append(arguments, base+".."+head)
	}
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list commits: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return nil, fmt.Errorf("no commits to check")
	}
	return strings.Fields(string(output)), nil
}

func validRevision(revision string) bool {
	if revision == "HEAD" {
		return true
	}
	if len(revision) != 40 && len(revision) != 64 {
		return false
	}
	for _, character := range revision {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func parseCommitIdentities(object string) ([]commitIdentity, error) {
	headers, message, found := strings.Cut(object, "\n\n")
	if !found {
		return nil, fmt.Errorf("commit has no message separator")
	}
	identities := make([]commitIdentity, 0, 3)
	authorFound, committerFound := false, false
	for _, line := range strings.Split(headers, "\n") {
		for _, role := range []string{"author", "committer"} {
			if !strings.HasPrefix(line, role+" ") {
				continue
			}
			if role == "author" {
				if authorFound {
					return nil, fmt.Errorf("commit has multiple authors")
				}
				authorFound = true
			} else {
				if committerFound {
					return nil, fmt.Errorf("commit has multiple committers")
				}
				committerFound = true
			}
			email, err := identityEmail(strings.TrimPrefix(line, role+" "))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", role, err)
			}
			identities = append(identities, commitIdentity{role: role, email: email})
		}
	}
	if !authorFound || !committerFound {
		return nil, fmt.Errorf("commit must have one author and one committer")
	}
	for _, line := range strings.Split(message, "\n") {
		key, address, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(key, "Co-authored-by") {
			continue
		}
		email, err := identityEmail(address)
		if err != nil {
			return nil, fmt.Errorf("Co-authored-by: %w", err)
		}
		identities = append(identities, commitIdentity{role: "Co-authored-by", email: email})
	}
	return identities, nil
}

func identityEmail(identity string) (string, error) {
	opening := strings.LastIndexByte(identity, '<')
	closing := strings.LastIndexByte(identity, '>')
	if opening < 0 || closing <= opening+1 {
		return "", fmt.Errorf("identity has no email address")
	}
	return identity[opening+1 : closing], nil
}

func checkNoreplyIdentity(identity commitIdentity, userID func(string) (int64, error)) error {
	local, domain, found := strings.Cut(identity.email, "@")
	if !found || !strings.EqualFold(domain, "users.noreply.github.com") {
		return nil
	}
	idText, login, found := strings.Cut(local, "+")
	if !found || idText == "" || idText[0] < '0' || idText[0] > '9' {
		return nil
	}
	if login == "" {
		return fmt.Errorf("%s: GitHub noreply login is empty", identity.role)
	}
	claimedID, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || claimedID <= 0 {
		return fmt.Errorf("%s: invalid GitHub noreply ID in %q", identity.role, identity.email)
	}
	actualID, err := userID(login)
	if err != nil {
		return fmt.Errorf("%s: verify GitHub user %q: %w", identity.role, login, err)
	}
	if claimedID != actualID {
		return fmt.Errorf("%s: %q claims GitHub ID %d, but %s has ID %d", identity.role, identity.email, claimedID, login, actualID)
	}
	return nil
}

func (lookup *githubLookup) userID(login string) (int64, error) {
	key := strings.ToLower(login)
	if id := lookup.resolved[key]; id != 0 {
		return id, nil
	}
	request, err := http.NewRequest(http.MethodGet, lookup.apiURL+"/users/"+url.PathEscape(login), nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if lookup.token != "" {
		request.Header.Set("Authorization", "Bearer "+lookup.token)
	}
	response, err := lookup.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub API returned %s", response.Status)
	}
	var account struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&account); err != nil {
		return 0, err
	}
	if account.ID <= 0 || !strings.EqualFold(account.Login, login) {
		return 0, fmt.Errorf("GitHub API returned an unexpected account for %q", login)
	}
	lookup.resolved[key] = account.ID
	return account.ID, nil
}
