package architecture

import (
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
