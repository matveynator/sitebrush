package architecture

import (
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGitHubActionsUseImmutableCommitReferences(t *testing.T) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}

	violations, err := findUnpinnedActionReferences(filepath.Join(moduleRoot, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestStableReleaseWorkflowFailsClosedAndUsesArtifactAllowlist(t *testing.T) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	workflowPath := filepath.Join(moduleRoot, ".github", "workflows", "release.yml")
	workflowBytes, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)

	if !strings.Contains(workflow, "set -euo pipefail") {
		t.Fatal("release assembly must fail on an unchecked command")
	}
	if strings.Count(workflow, "if-no-files-found: error") != 3 {
		t.Fatal("release workflow must fail when each of the three build artifact sets is empty")
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

func TestActionPinCheckRejectsMutableTagsAndBranches(t *testing.T) {
	directory := t.TempDir()
	workflow := "steps:\n  - uses: actions/checkout@v6\n  - uses: owner/action@main\n  - uses: ./local-action\n  - uses: docker://alpine:3.22\n"
	if err := os.WriteFile(filepath.Join(directory, "test.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}

	violations, err := findUnpinnedActionReferences(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 2 {
		t.Fatalf("found %d violations, want 2: %v", len(violations), violations)
	}
}

func TestActionPinCheckAcceptsCommitSHA(t *testing.T) {
	directory := t.TempDir()
	workflow := "steps:\n  - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803 # v6\n"
	if err := os.WriteFile(filepath.Join(directory, "test.yaml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}

	violations, err := findUnpinnedActionReferences(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("found unexpected violations: %v", violations)
	}
}

func TestFirstPartyCodeDoesNotUseLockPrimitives(t *testing.T) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}

	violations, err := findLockPrimitiveViolations(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestLockPrimitiveCheckFindsForbiddenSelectors(t *testing.T) {
	moduleRoot := t.TempDir()
	writeGoFile(t, moduleRoot, `package fixture

import synchronization "sync"

var mutex synchronization.Mutex
var readWriteMutex synchronization.RWMutex
var once synchronization.Once
var locker synchronization.Locker
`)

	violations, err := findLockPrimitiveViolations(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 4 {
		t.Fatalf("found %d violations, want 4: %v", len(violations), violations)
	}
}

func TestLockPrimitiveCheckAllowsOtherSyncTypes(t *testing.T) {
	moduleRoot := t.TempDir()
	writeGoFile(t, moduleRoot, `package fixture

import "sync"

// sync.Mutex in a comment is not a lock primitive declaration.
const documentation = "sync.RWMutex"
var waitGroup sync.WaitGroup
`)

	violations, err := findLockPrimitiveViolations(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("found unexpected violations: %v", violations)
	}
}

func TestLockPrimitiveCheckRejectsSyncDotImport(t *testing.T) {
	moduleRoot := t.TempDir()
	writeGoFile(t, moduleRoot, `package fixture

import . "sync"

var waitGroup WaitGroup
`)

	violations, err := findLockPrimitiveViolations(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "dot-imports sync") {
		t.Fatalf("found violations %v, want one sync dot-import violation", violations)
	}
}

// The architecture test runs from its package directory, so it must locate the
// module explicitly before enforcing a repository-wide invariant.
func findModuleRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	currentDirectory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		moduleFile := filepath.Join(currentDirectory, "go.mod")
		moduleFileInfo, statErr := os.Stat(moduleFile)
		if statErr == nil && !moduleFileInfo.IsDir() {
			return currentDirectory, nil
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect %s: %w", moduleFile, statErr)
		}

		parentDirectory := filepath.Dir(currentDirectory)
		if parentDirectory == currentDirectory {
			return "", fmt.Errorf("find module root from %s: go.mod not found", workingDirectory)
		}
		currentDirectory = parentDirectory
	}
}

func findUnpinnedActionReferences(workflowDirectory string) ([]string, error) {
	violations := make([]string, 0)
	err := filepath.WalkDir(workflowDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".yml" && extension != ".yaml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for lineNumber, line := range strings.Split(string(data), "\n") {
			trimmedLine := strings.TrimSpace(line)
			trimmedLine = strings.TrimSpace(strings.TrimPrefix(trimmedLine, "-"))
			if !strings.HasPrefix(trimmedLine, "uses:") {
				continue
			}
			reference := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "uses:"))
			if commentIndex := strings.Index(reference, " #"); commentIndex >= 0 {
				reference = strings.TrimSpace(reference[:commentIndex])
			}
			if strings.HasPrefix(reference, "./") || strings.HasPrefix(reference, "docker://") {
				continue
			}
			atIndex := strings.LastIndex(reference, "@")
			if atIndex <= 0 || !isCommitSHA(reference[atIndex+1:]) {
				violations = append(violations, fmt.Sprintf("%s:%d uses mutable action reference %q", filepath.Base(path), lineNumber+1, reference))
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan GitHub Actions workflows: %w", err)
	}
	return violations, nil
}

func isCommitSHA(reference string) bool {
	if len(reference) != 40 {
		return false
	}
	_, err := hex.DecodeString(reference)
	return err == nil
}

func findLockPrimitiveViolations(moduleRoot string) ([]string, error) {
	violations := make([]string, 0)
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		relativePath, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		fileViolations, err := inspectGoFile(path, filepath.ToSlash(relativePath))
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan first-party Go code: %w", err)
	}
	return violations, nil
}

func inspectGoFile(path string, relativePath string) ([]string, error) {
	parsedFile, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relativePath, err)
	}

	violations := make([]string, 0)
	syncImportNames := make(map[string]bool)
	for _, importedPackage := range parsedFile.Imports {
		importPath, err := strconv.Unquote(importedPackage.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("parse import in %s: %w", relativePath, err)
		}
		if importPath != "sync" {
			continue
		}

		importName := "sync"
		if importedPackage.Name != nil {
			importName = importedPackage.Name.Name
		}
		if importName == "." {
			violations = append(violations, fmt.Sprintf("%s dot-imports sync, which bypasses the lock primitive check", relativePath))
			continue
		}
		if importName != "_" {
			syncImportNames[importName] = true
		}
	}

	ast.Inspect(parsedFile, func(node ast.Node) bool {
		selector, selectorFound := node.(*ast.SelectorExpr)
		if !selectorFound || !isForbiddenSyncPrimitive(selector.Sel.Name) {
			return true
		}
		packageIdentifier, identifierFound := selector.X.(*ast.Ident)
		if identifierFound && syncImportNames[packageIdentifier.Name] {
			violations = append(violations, fmt.Sprintf("%s uses forbidden lock primitive %s.%s", relativePath, packageIdentifier.Name, selector.Sel.Name))
		}
		return true
	})
	return violations, nil
}

func isForbiddenSyncPrimitive(name string) bool {
	switch name {
	case "Locker", "Mutex", "Once", "RWMutex":
		return true
	default:
		return false
	}
}

func writeGoFile(t *testing.T, directory string, source string) {
	t.Helper()
	path := filepath.Join(directory, "fixture.go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}
