package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDockerPlatformAndBuilderOrchestration(t *testing.T) {
	binDirectory := t.TempDir()
	dockerPath := filepath.Join(binDirectory, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\ncase \"$1:$2\" in\nimage:inspect) [ \"$DOCKER_IMAGE_MISSING\" = 1 ] && exit 1; exit 0 ;;\ninfo:*) [ \"$DOCKER_INFO_FAIL\" = 1 ] && exit 1; echo x86_64 ;;\nrun:*) [ \"$DOCKER_RUN_FAIL\" = 1 ] && exit 1; [ \"$DOCKER_EMULATION_FAIL\" = 1 ] && [ \"$2\" = --privileged ] && exit 1; exit 0 ;;\nbuild:*) [ \"$DOCKER_BUILD_FAIL\" = 1 ] && exit 1; cat >/dev/null ;;\nesac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDirectory+string(os.PathListSeparator)+originalPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("PATH", originalPath) })
	for _, name := range []string{"DOCKER_IMAGE_MISSING", "DOCKER_INFO_FAIL", "DOCKER_RUN_FAIL", "DOCKER_EMULATION_FAIL", "DOCKER_BUILD_FAIL"} {
		t.Setenv(name, "")
	}

	root := t.TempDir()
	if err := ensureDockerPlatformSupport(root, "linux/amd64", "cached-image"); err != nil {
		t.Fatalf("cached native image support: %v", err)
	}
	if err := ensureDockerPlatformSupport(root, "linux/arm64", "uncached-image"); err != nil {
		t.Fatalf("register emulation for missing image: %v", err)
	}
	if err := ensureDockerBuilderImage(root, "linux/amd64", "cached-image", "FROM scratch", false); err != nil {
		t.Fatalf("reuse cached builder: %v", err)
	}
	if err := ensureDockerBuilderImage(root, "linux/amd64", "cached-image", "FROM scratch", true); err != nil {
		t.Fatalf("rebuild builder: %v", err)
	}
	if err := os.Setenv("DOCKER_IMAGE_MISSING", "1"); err != nil {
		t.Fatal(err)
	}
	if err := ensureDockerPlatformSupport(root, "linux/amd64", "missing-image"); err != nil {
		t.Fatalf("native daemon platform should need no emulation: %v", err)
	}
	if err := os.Setenv("DOCKER_INFO_FAIL", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := dockerNativePlatform(root); err == nil {
		t.Fatal("Docker info failure was ignored")
	}
	if err := os.Setenv("DOCKER_INFO_FAIL", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DOCKER_RUN_FAIL", "1"); err != nil {
		t.Fatal(err)
	}
	if err := ensureDockerPlatformSupport(root, "linux/arm64", "cached-image"); err == nil {
		t.Fatal("failed emulation probe was ignored")
	}
	if err := os.Setenv("DOCKER_RUN_FAIL", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DOCKER_EMULATION_FAIL", "1"); err != nil {
		t.Fatal(err)
	}
	if err := installDockerPlatformEmulation(root, "linux/arm64", "arm64"); err == nil {
		t.Fatal("emulation registration failure was ignored")
	}
	if err := os.Setenv("DOCKER_EMULATION_FAIL", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DOCKER_BUILD_FAIL", "1"); err != nil {
		t.Fatal(err)
	}
	if err := ensureDockerBuilderImage(root, "linux/amd64", "missing-image", "FROM scratch", true); err == nil {
		t.Fatal("Docker build failure was ignored")
	}
}
