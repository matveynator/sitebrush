//go:build !windows

package systeminit

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type kernelSetting struct {
	name   string
	target uint64
}

// ApplyKernelTuning runs before the service drops privileges. Kernel controls
// are best effort because supported limits vary by OS version and host size.
func ApplyKernelTuning() {
	settings := []kernelSetting{}
	switch runtime.GOOS {
	case "linux":
		settings = []kernelSetting{{"fs.file-max", 4194304}, {"net.core.somaxconn", 65535}, {"net.ipv4.tcp_max_syn_backlog", 65535}}
	case "openbsd":
		settings = []kernelSetting{{"kern.maxfiles", 4194304}, {"kern.somaxconn", 65535}}
	case "freebsd":
		settings = []kernelSetting{{"kern.maxfiles", 4194304}, {"kern.ipc.soacceptqueue", 65535}}
	case "netbsd", "darwin":
		settings = []kernelSetting{{"kern.maxfiles", 4194304}}
	}
	for _, setting := range settings {
		before, err := readKernelSetting(setting.name)
		if err != nil {
			log.Printf("SYSTEM TUNING %s skipped: %v", setting.name, err)
			continue
		}
		if before >= setting.target {
			log.Printf("SYSTEM TUNING %s current=%d target=%d unchanged", setting.name, before, setting.target)
			continue
		}
		if err := writeKernelSetting(setting.name, setting.target); err != nil {
			log.Printf("SYSTEM TUNING %s current=%d target=%d rejected: %v", setting.name, before, setting.target, err)
			continue
		}
		after, err := readKernelSetting(setting.name)
		if err != nil {
			log.Printf("SYSTEM TUNING %s target=%d verification failed: %v", setting.name, setting.target, err)
			continue
		}
		log.Printf("SYSTEM TUNING %s before=%d target=%d after=%d", setting.name, before, setting.target, after)
	}
}

func readKernelSetting(name string) (uint64, error) {
	if runtime.GOOS == "linux" {
		content, err := os.ReadFile(filepath.Join("/proc/sys", strings.ReplaceAll(name, ".", "/")))
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	}
	output, err := exec.Command("sysctl", "-n", name).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
}

func writeKernelSetting(name string, target uint64) error {
	if runtime.GOOS == "linux" {
		return os.WriteFile(filepath.Join("/proc/sys", strings.ReplaceAll(name, ".", "/")), []byte(strconv.FormatUint(target, 10)), 0)
	}
	output, err := exec.Command("sysctl", fmt.Sprintf("%s=%d", name, target)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
