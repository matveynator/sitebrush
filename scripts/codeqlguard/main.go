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

// SARIF parsing is intentionally minimal: the gate only needs security result rule IDs
// and suppression metadata, while GitHub remains the canonical viewer for locations.
type sarifLog struct {
	Runs []sarifRun `json:"runs"`
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

// scanDirectory reads every SARIF file produced by one CodeQL analysis job.
// It never reports source locations so a public CI log does not become a vulnerability index.
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

// scanFile counts only unsuppressed results. Explicitly suppressed CodeQL findings
// remain visible to CodeQL policy, but do not make this independent gate disagree with it.
func scanFile(path string) (findingSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return findingSummary{}, err
	}

	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return findingSummary{}, fmt.Errorf("decode SARIF: %w", err)
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
