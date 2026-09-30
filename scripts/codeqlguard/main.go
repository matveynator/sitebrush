package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const codeQLResultsDirectory = "codeql-results"

type sarifLog struct {
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	RuleID       string             `json:"ruleId"`
	Suppressions []sarifSuppression `json:"suppressions"`
}

type sarifSuppression struct {
	Status string `json:"status"`
}

type findingSummary struct {
	Total  int
	ByRule map[string]int
}

type validatedFinding struct {
	RuleID        string
	URI           string
	LineHash      string
	StartLine     int
	Justification string
}

var validatedBaseline = []validatedFinding{
	{RuleID: "go/bad-redirect-check", URI: "pkg/crawler/whole_site.go", StartLine: 101, Justification: "Crawler paths are local-only and pass through LocalRedirectTarget, which rejects network-path, host, credential, backslash, and control-character forms."},
	{RuleID: "go/bad-redirect-check", URI: "sitebrush.go", StartLine: 14540, Justification: "Application paths are local-only and pass through LocalRedirectTarget, which independently enforces same-origin redirect syntax."},
	{RuleID: "go/path-injection", URI: "sitebrush.go", LineHash: "a2e340fc87446ad4:1", Justification: "Path starts under the site storage root and is revalidated after symlink resolution."},
	{RuleID: "go/path-injection", URI: "sitebrush.go", LineHash: "d47546112b0da682:1", Justification: "The existing parent is derived only by walking parents of a storage-root-derived candidate."},
	{RuleID: "go/path-injection", URI: "sitebrush.go", LineHash: "b38a944104bd3220:1", Justification: "writablePathInsideStorageSubtree constrains the directory to the domain storage root before creation."},
	{RuleID: "go/path-injection", URI: "sitebrush.go", LineHash: "d15d3c8a5f9dc31d:1", Justification: "The path is canonicalized with EvalSymlinks and checked with isPathWithinRoot before acceptance."},
	{RuleID: "go/path-injection", URI: "sitebrush.go", LineHash: "e74d682a7512547d:1", Justification: "The chroot target is canonicalized and checked against the domain root before file metadata access."},
	{RuleID: "go/path-injection", URI: "sitebrush.go", LineHash: "f8e148382f7697c3:1", Justification: "Callers pass only directory paths returned by the validated chroot resolver."},
	{RuleID: "go/request-forgery", URI: "pkg/crawler/download.go", LineHash: "1b25405598db72a4:1", Justification: "RequirePublicURL rejects unsafe targets and the outbound transport re-resolves and pins every dial to public IP addresses."},
}

func scanDirectory(directory string) (findingSummary, error) {
	summary := findingSummary{ByRule: map[string]int{}}
	foundSARIF := false

	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".sarif") {
			return nil
		}
		foundSARIF = true
		if err := applyValidatedBaseline(path); err != nil {
			return err
		}
		fileSummary, err := scanFile(path)
		if err != nil {
			return err
		}
		summary.Total += fileSummary.Total
		for ruleID, count := range fileSummary.ByRule {
			summary.ByRule[ruleID] += count
		}
		return nil
	})
	if err != nil {
		return findingSummary{}, err
	}
	if !foundSARIF {
		return findingSummary{}, errors.New("CodeQL produced no SARIF file")
	}
	return summary, nil
}

// applyValidatedBaseline removes only exact CodeQL results that have a reviewed
// security boundary. A moved or newly introduced finding gets a different fingerprint
// or line and therefore remains in SARIF and fails CI.
func applyValidatedBaseline(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode SARIF: %w", err)
	}
	if document["version"] != "2.1.0" {
		return fmt.Errorf("invalid SARIF version %q", document["version"])
	}
	runs, ok := document["runs"].([]any)
	if !ok || len(runs) == 0 {
		return errors.New("SARIF contains no runs")
	}

	for _, runValue := range runs {
		run, _ := runValue.(map[string]any)
		results, _ := run["results"].([]any)
		filteredResults := make([]any, 0, len(results))
		for _, resultValue := range results {
			result, _ := resultValue.(map[string]any)
			ruleID, _ := result["ruleId"].(string)
			uri := primaryResultURI(result)
			lineHash := primaryLineHash(result)
			startLine := primaryStartLine(result)
			_, matched := baselineJustification(ruleID, uri, lineHash, startLine)
			if matched {
				continue
			}
			filteredResults = append(filteredResults, resultValue)
		}
		run["results"] = filteredResults
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode SARIF: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return err
	}
	return nil
}

func primaryResultURI(result map[string]any) string {
	locations, _ := result["locations"].([]any)
	if len(locations) == 0 {
		return ""
	}
	location, _ := locations[0].(map[string]any)
	physical, _ := location["physicalLocation"].(map[string]any)
	artifact, _ := physical["artifactLocation"].(map[string]any)
	uri, _ := artifact["uri"].(string)
	return uri
}

func primaryStartLine(result map[string]any) int {
	locations, _ := result["locations"].([]any)
	if len(locations) == 0 {
		return 0
	}
	location, _ := locations[0].(map[string]any)
	physical, _ := location["physicalLocation"].(map[string]any)
	region, _ := physical["region"].(map[string]any)
	startLine, _ := region["startLine"].(float64)
	return int(startLine)
}

func primaryLineHash(result map[string]any) string {
	fingerprints, _ := result["partialFingerprints"].(map[string]any)
	lineHash, _ := fingerprints["primaryLocationLineHash"].(string)
	return lineHash
}

func baselineJustification(ruleID, uri, lineHash string, startLine int) (string, bool) {
	for _, finding := range validatedBaseline {
		if finding.RuleID != ruleID || finding.URI != uri {
			continue
		}
		if finding.LineHash != "" && finding.LineHash == lineHash {
			return finding.Justification, true
		}
		if finding.LineHash == "" && finding.StartLine > 0 && finding.StartLine == startLine {
			return finding.Justification, true
		}
	}
	return "", false
}

func scanFile(path string) (findingSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return findingSummary{}, err
	}

	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return findingSummary{}, fmt.Errorf("decode SARIF: %w", err)
	}
	if log.Version != "2.1.0" {
		return findingSummary{}, fmt.Errorf("invalid SARIF version %q", log.Version)
	}
	if len(log.Runs) == 0 {
		return findingSummary{}, errors.New("SARIF contains no runs")
	}

	summary := findingSummary{ByRule: map[string]int{}}
	for _, run := range log.Runs {
		for _, result := range run.Results {
			if resultSuppressed(result) {
				continue
			}
			ruleID := strings.TrimSpace(result.RuleID)
			if ruleID == "" {
				ruleID = "unknown-rule"
			}
			summary.Total++
			summary.ByRule[ruleID]++
		}
	}
	return summary, nil
}

func resultSuppressed(result sarifResult) bool {
	for _, suppression := range result.Suppressions {
		status := strings.ToLower(strings.TrimSpace(suppression.Status))
		if status == "" || status == "accepted" {
			return true
		}
	}
	return false
}

func sortedRules(summary findingSummary) []string {
	rules := make([]string, 0, len(summary.ByRule))
	for ruleID := range summary.ByRule {
		rules = append(rules, ruleID)
	}
	sort.Strings(rules)
	return rules
}

func main() {
	summary, err := scanDirectory(codeQLResultsDirectory)
	if err != nil {
		fmt.Fprintln(os.Stderr, "CodeQL gate:", err)
		os.Exit(1)
	}
	if summary.Total == 0 {
		fmt.Println("CodeQL gate: no unsuppressed findings")
		return
	}

	fmt.Fprintf(os.Stderr, "CodeQL gate: %d unsuppressed finding(s) across %d rule(s)\n", summary.Total, len(summary.ByRule))
	for _, ruleID := range sortedRules(summary) {
		fmt.Fprintf(os.Stderr, "  %s: %d\n", ruleID, summary.ByRule[ruleID])
	}
	fmt.Fprintln(os.Stderr, "Inspect the repository Code Scanning page for protected source locations and data-flow details.")
	os.Exit(1)
}
