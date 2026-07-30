package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicSensitiveRequestRedirectsToHTTPSBeforeApplicationWork(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://public.example/account?login", strings.NewReader("email=user%40example.com"))
	response := httptest.NewRecorder()
	(&App{}).route(response, request)
	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTemporaryRedirect)
	}
	if location := response.Header().Get("Location"); location != "https://public.example/account?login" {
		t.Fatalf("Location = %q", location)
	}
}

func TestHTTPRedirectAndCookieBoundariesCannotBeBypassed(t *testing.T) {
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(".", func(filePath string, directoryEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if directoryEntry.IsDir() {
			if filePath == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") || strings.HasPrefix(filepath.ToSlash(filePath), "pkg/httpsecurity/") {
			return nil
		}
		parsedFile, parseErr := parser.ParseFile(fileSet, filePath, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsedFile, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector || (selector.Sel.Name != "Redirect" && selector.Sel.Name != "SetCookie") {
				return true
			}
			packageIdentifier, isIdentifier := selector.X.(*ast.Ident)
			if !isIdentifier || packageIdentifier.Name != "http" {
				return true
			}
			position := fileSet.Position(call.Pos())
			t.Errorf("%s calls http.%s directly; use pkg/httpsecurity", position, selector.Sel.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
