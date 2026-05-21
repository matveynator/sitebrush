//go:build netbsd

package serviceinstall

import (
	"os/exec"
	"strconv"
	"strings"
)

func diskSpace(path string) (uint64, uint64, bool) {
	output, err := exec.Command("df", "-k", path).CombinedOutput()
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Fields(string(output))
	if len(lines) < 6 {
		return 0, 0, false
	}
	totalKB, err := strconv.ParseUint(lines[len(lines)-5], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	freeKB, err := strconv.ParseUint(lines[len(lines)-3], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return freeKB * 1024, totalKB * 1024, totalKB > 0
}
