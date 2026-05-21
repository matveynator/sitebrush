package serviceinstall

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func fakeProbe(goos string, commands map[string]bool, paths map[string]bool) runtimeProbe {
	return runtimeProbe{
		goos:      goos,
		goarch:    "amd64",
		osVersion: "test",
		lookPath: func(name string) (string, error) {
			if commands[name] {
				return "/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		fileExists: func(path string) bool {
			return paths[path]
		},
		dirExists: func(path string) bool {
			return paths[path]
		},
		commandRuns: func(ctx context.Context, name string, args ...string) (string, error) {
			if commands[name] {
				return "", nil
			}
			return "", errors.New("command failed")
		},
	}
}

func TestDetectLinuxServiceManagersFromRuntimeChecks(t *testing.T) {
	testCases := []struct {
		name     string
		commands map[string]bool
		paths    map[string]bool
		want     string
	}{
		{
			name:     "systemd",
			commands: map[string]bool{"systemctl": true},
			paths:    map[string]bool{"/run/systemd/system": true},
			want:     "systemd",
		},
		{
			name:     "openrc",
			commands: map[string]bool{"rc-service": true, "rc-update": true},
			paths:    map[string]bool{"/run/openrc": true, "/etc/init.d": true},
			want:     "OpenRC",
		},
		{
			name:     "runit",
			commands: map[string]bool{"sv": true, "runsvdir": true},
			paths:    map[string]bool{"/etc/sv": true, "/etc/service": true},
			want:     "runit",
		},
		{
			name:     "upstart",
			commands: map[string]bool{"initctl": true},
			paths:    map[string]bool{"/etc/init": true},
			want:     "Upstart",
		},
		{
			name:     "sysv",
			commands: map[string]bool{"service": true, "update-rc.d": true},
			paths:    map[string]bool{"/etc/init.d": true},
			want:     "SysV init",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			manager, err := detectServiceManager(fakeProbe("linux", testCase.commands, testCase.paths))
			if err != nil {
				t.Fatalf("detectServiceManager: %v", err)
			}
			if manager.Name != testCase.want {
				t.Fatalf("manager = %q, want %q", manager.Name, testCase.want)
			}
		})
	}
}

func TestDetectPlatformServiceManagers(t *testing.T) {
	testCases := []struct {
		goos     string
		commands map[string]bool
		paths    map[string]bool
		want     string
	}{
		{"darwin", map[string]bool{"launchctl": true}, map[string]bool{"/Library/LaunchDaemons": true}, "launchd"},
		{"windows", map[string]bool{"sc.exe": true}, nil, "Windows Service"},
		{"freebsd", map[string]bool{"service": true}, map[string]bool{"/etc/rc.d": true}, "rc.d"},
		{"openbsd", map[string]bool{"rcctl": true}, map[string]bool{"/etc/rc.d": true}, "rc.d/rcctl"},
		{"netbsd", map[string]bool{"service": true}, map[string]bool{"/etc/rc.d": true}, "rc.d"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.goos, func(t *testing.T) {
			manager, err := detectServiceManager(fakeProbe(testCase.goos, testCase.commands, testCase.paths))
			if err != nil {
				t.Fatalf("detectServiceManager: %v", err)
			}
			if manager.Name != testCase.want {
				t.Fatalf("manager = %q, want %q", manager.Name, testCase.want)
			}
		})
	}
}

func TestDetectServiceManagerRejectsUnsupportedRuntime(t *testing.T) {
	_, err := detectServiceManager(fakeProbe("linux", map[string]bool{"systemctl": true}, nil))
	if err == nil {
		t.Fatal("systemctl without /run/systemd/system should not be accepted")
	}
	if !strings.Contains(err.Error(), "no supported service/init system") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGeneratedServiceFilesContainSitebrushCommand(t *testing.T) {
	plan := installPlan{
		ServiceName: "sitebrush",
		BinaryPath:  "/usr/local/bin/sitebrush",
		WorkingDir:  "/var/lib/sitebrush",
		ExecArgs:    []string{"/usr/local/bin/sitebrush", "-port", "80,443", "-storage-path", "/var/lib/sitebrush", "-db-type", "sqlite", "-db-path", "/var/lib/sitebrush/storage/db/sitebrush.db"},
	}
	for name, content := range map[string]string{
		"sysv":    sysVRcScript(plan),
		"freebsd": freeBSDRcScript(plan),
		"openbsd": openBSDRcScript(plan),
		"netbsd":  netBSDRcScript(plan),
		"launchd": launchdPlist("net.sitebrush.sitebrush", plan),
	} {
		if !strings.Contains(content, "/usr/local/bin/sitebrush") {
			t.Fatalf("%s script missing binary path:\n%s", name, content)
		}
		if !strings.Contains(content, "-storage-path") {
			t.Fatalf("%s script missing server args:\n%s", name, content)
		}
	}
}

func TestBuildInstallPlanUsesInstallFlagFreeExecArgs(t *testing.T) {
	plan, err := buildInstallPlan(Options{
		Port:        "8080",
		StoragePath: "/var/lib/sitebrush",
		DBType:      "sqlite",
		DBPath:      "/var/lib/sitebrush/storage/db/sitebrush.db",
		BinaryPath:  "/tmp/sitebrush",
		ServiceName: "sitebrush",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(plan.ExecArgs, " ")
	for _, forbiddenFlag := range []string{"-install", "-set" + "up"} {
		if strings.Contains(got, forbiddenFlag) {
			t.Fatalf("service command should not include control flag %q: %s", forbiddenFlag, got)
		}
	}
	if !strings.Contains(got, "-port 8080") {
		t.Fatalf("service command missing selected port: %s", got)
	}
}

func TestInteractiveWizardEditsPlanBeforeApply(t *testing.T) {
	var output strings.Builder
	options, err := runInteractiveWizard(
		context.Background(),
		Options{
			Port:        "80,443",
			StoragePath: "/var/lib/sitebrush",
			DBType:      "sqlite",
			DBPath:      "/var/lib/sitebrush/storage/db/sitebrush.db",
			BinaryPath:  "/tmp/sitebrush",
			Input:       strings.NewReader("en\n2\n9090\ncontinue\n"),
			Output:      &output,
		},
		fakeProbe("linux", map[string]bool{"systemctl": true}, map[string]bool{"/run/systemd/system": true}),
		serviceManager{Name: "systemd"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.Port != "9090" {
		t.Fatalf("port = %q, want 9090", options.Port)
	}
	if !strings.Contains(output.String(), "Sitebrush service install") || !strings.Contains(output.String(), "service system: systemd") || !strings.Contains(output.String(), "Disk space") {
		t.Fatalf("wizard output did not explain detected service system:\n%s", output.String())
	}
}

func TestInteractiveWizardCanCancel(t *testing.T) {
	_, err := runInteractiveWizard(
		context.Background(),
		Options{Input: strings.NewReader("en\nquit\n"), Output: io.Discard},
		fakeProbe("linux", nil, nil),
		serviceManager{Name: "systemd"},
	)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestInteractiveWizardCanUseRussian(t *testing.T) {
	var output strings.Builder
	_, err := runInteractiveWizard(
		context.Background(),
		Options{BinaryPath: "/tmp/sitebrush", Input: strings.NewReader("ru\nq\n"), Output: &output},
		fakeProbe("linux", map[string]bool{"systemctl": true}, map[string]bool{"/run/systemd/system": true}),
		serviceManager{Name: "systemd"},
	)
	if err == nil || !strings.Contains(err.Error(), "отменена") {
		t.Fatalf("cancel error = %v", err)
	}
	if !strings.Contains(output.String(), "Установка службы Sitebrush") || !strings.Contains(output.String(), "Место на диске") {
		t.Fatalf("wizard output is not localized:\n%s", output.String())
	}
}

func TestInteractiveWizardAcceptsLocalizedDefaultAction(t *testing.T) {
	options, err := runInteractiveWizard(
		context.Background(),
		Options{BinaryPath: "/tmp/sitebrush", Input: strings.NewReader("ru\nпродолжить\n"), Output: io.Discard},
		fakeProbe("linux", map[string]bool{"systemctl": true}, map[string]bool{"/run/systemd/system": true}),
		serviceManager{Name: "systemd"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.Language != "ru" {
		t.Fatalf("language = %q, want ru", options.Language)
	}
}

func TestInteractiveUninstallWizardEditsServiceName(t *testing.T) {
	var output strings.Builder
	options, err := runInteractiveUninstallWizard(
		context.Background(),
		Options{BinaryPath: "/tmp/sitebrush", Input: strings.NewReader("en\n1\nsitebrush-test\nuninstall\n"), Output: &output},
		fakeProbe("linux", map[string]bool{"systemctl": true}, map[string]bool{"/run/systemd/system": true}),
		serviceManager{Name: "systemd"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.ServiceName != "sitebrush-test" {
		t.Fatalf("service name = %q, want sitebrush-test", options.ServiceName)
	}
	if !strings.Contains(output.String(), "Sitebrush service uninstall") || !strings.Contains(output.String(), "/etc/systemd/system/sitebrush.service") {
		t.Fatalf("wizard output did not explain uninstall plan:\n%s", output.String())
	}
}

func TestInteractiveUninstallWizardCanCancel(t *testing.T) {
	_, err := runInteractiveUninstallWizard(
		context.Background(),
		Options{Input: strings.NewReader("en\nquit\n"), Output: io.Discard},
		fakeProbe("linux", nil, nil),
		serviceManager{Name: "systemd"},
	)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestInteractiveUninstallWizardEnterKeepsServiceByDefault(t *testing.T) {
	_, err := runInteractiveUninstallWizard(
		context.Background(),
		Options{Language: "ru", BinaryPath: "/tmp/sitebrush", Input: strings.NewReader("ru\n\n"), Output: io.Discard},
		fakeProbe("linux", map[string]bool{"systemctl": true}, map[string]bool{"/run/systemd/system": true}),
		serviceManager{Name: "systemd"},
	)
	if err == nil || !strings.Contains(err.Error(), "отменено") {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestInteractiveUninstallWizardAcceptsLocalizedRemoveAction(t *testing.T) {
	options, err := runInteractiveUninstallWizard(
		context.Background(),
		Options{Language: "ru", BinaryPath: "/tmp/sitebrush", Input: strings.NewReader("ru\nудалить\n"), Output: io.Discard},
		fakeProbe("linux", map[string]bool{"systemctl": true}, map[string]bool{"/run/systemd/system": true}),
		serviceManager{Name: "systemd"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.Language != "ru" {
		t.Fatalf("language = %q, want ru", options.Language)
	}
}

func TestCLITranslationsDoNotFallbackToEnglishForInterfaceActions(t *testing.T) {
	english := cliTextForLanguage("en")
	for _, languageCode := range cliLanguageOrder {
		if languageCode == "en" {
			continue
		}
		text := cliTextForLanguage(languageCode)
		checks := map[string][2]string{
			"language title":        {text.LanguageTitle, english.LanguageTitle},
			"language prompt":       {text.LanguagePrompt, english.LanguagePrompt},
			"uninstall help":        {text.UninstallHelp, english.UninstallHelp},
			"uninstall prompt":      {text.UninstallPrompt, english.UninstallPrompt},
			"uninstall default":     {text.UninstallDefaultAction, english.UninstallDefaultAction},
			"keep option":           {text.UninstallKeepOption, english.UninstallKeepOption},
			"remove option":         {text.UninstallRemoveOption, english.UninstallRemoveOption},
			"uninstall select help": {text.UninstallSelectHelp, english.UninstallSelectHelp},
			"install complete":      {text.InstallComplete, english.InstallComplete},
			"uninstall complete":    {text.UninstallComplete, english.UninstallComplete},
			"commands":              {text.CommandsLabel, english.CommandsLabel},
			"binary left":           {text.BinaryLeftLabel, english.BinaryLeftLabel},
			"disk free":             {text.DiskSpaceFreeOf, english.DiskSpaceFreeOf},
			"disk unknown":          {text.DiskSpaceUnknown, english.DiskSpaceUnknown},
		}
		for label, values := range checks {
			if strings.TrimSpace(values[0]) == "" {
				t.Fatalf("%s %s is empty", languageCode, label)
			}
			if values[0] == values[1] {
				t.Fatalf("%s %s fell back to English %q", languageCode, label, values[0])
			}
		}
	}
}
