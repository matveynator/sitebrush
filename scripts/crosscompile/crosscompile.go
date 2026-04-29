package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	targets := []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"freebsd", "amd64"}, {"freebsd", "arm64"},
		{"openbsd", "amd64"}, {"openbsd", "arm64"},
	}
	for _, target := range targets {
		output := fmt.Sprintf("dist/goup-%s-%s", target.goos, target.goarch)
		if target.goos == "windows" {
			output += ".exe"
		}
		command := exec.Command("go", "build", "-o", output, "./")
		command.Env = append(os.Environ(), "GOOS="+target.goos, "GOARCH="+target.goarch)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			panic(err)
		}
	}
}
