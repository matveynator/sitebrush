package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStableReleaseWorkflowFailsClosedAndUsesArtifactAllowlist(t *testing.T) {
	workflow := readRepositoryTestFile(t, filepath.Join(".github", "workflows", "release.yml"))

	if !strings.Contains(workflow, "set -euo pipefail") {
		t.Fatal("release assembly must fail on an unchecked command")
	}
	if strings.Count(workflow, "if-no-files-found: error") != 3 {
		t.Fatalf("release workflow must fail when each of the three build artifact sets is empty")
	}
	if !strings.Contains(workflow, "-name 'sitebrush_*'") ||
		!strings.Contains(workflow, "-name '*.zip'") ||
		!strings.Contains(workflow, "-name '*.dmg'") {
		t.Fatal("release assembly must copy only known binary artifact formats")
	}
	if strings.Contains(workflow, "-name '*'") || strings.Contains(workflow, "-name \"*\"") {
		t.Fatal("release assembly must not copy arbitrary downloaded artifact files")
	}
	if !strings.Contains(workflow, "md5sum sitebrush_* > MD5SUMS") {
		t.Fatal("release assembly must produce checksums for server binaries")
	}
	if !strings.Contains(workflow, "tag_name: stable-release") ||
		!strings.Contains(workflow, "overwrite_files: true") {
		t.Fatal("release must publish through the stable release tag")
	}
}
