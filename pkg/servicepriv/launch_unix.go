//go:build !windows

package servicepriv

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/matveynator/sitebrush/v2/pkg/serviceinstall"
	"github.com/matveynator/sitebrush/v2/pkg/systeminit"
)

// Launch owns only startup privileges and process lifetime. The worker owns
// all HTTP handling, databases, and update state.
func Launch(arguments []string, storagePath string, httpPort int, tlsEnabled bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("service startup needs root to prepare ports and drop privileges")
	}
	systeminit.ApplyKernelTuning()
	if err := systeminit.Init(); err != nil {
		return fmt.Errorf("prepare process limits: %w", err)
	}
	httpListener, err := net.Listen("tcp", ":"+strconv.Itoa(httpPort))
	if err != nil {
		return fmt.Errorf("open HTTP port %d: %w", httpPort, err)
	}
	defer httpListener.Close()
	files := make([]*os.File, 0, 2)
	httpFile, err := httpListener.(*net.TCPListener).File()
	if err != nil {
		return err
	}
	files = append(files, httpFile)
	defer httpFile.Close()
	if tlsEnabled {
		httpsListener, err := net.Listen("tcp", ":443")
		if err != nil {
			return fmt.Errorf("open HTTPS port 443: %w", err)
		}
		defer httpsListener.Close()
		httpsFile, err := httpsListener.(*net.TCPListener).File()
		if err != nil {
			return err
		}
		files = append(files, httpsFile)
		defer httpsFile.Close()
	}
	account, credentials, err := serviceCredentials()
	if err != nil {
		return err
	}
	workerArguments := make([]string, 0, len(arguments)+1)
	for _, argument := range arguments {
		if argument != "-service-runtime" {
			workerArguments = append(workerArguments, argument)
		}
	}
	workerArguments = append(workerArguments, "-service-worker")
	command := exec.Command(os.Args[0], workerArguments...)
	workingDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	command.Dir = workingDirectory
	command.ExtraFiles = files
	command.SysProcAttr = &syscall.SysProcAttr{Credential: credentials}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(filteredEnvironment(), "HOME="+storagePath, "USER="+account.Username, "LOGNAME="+account.Username)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start unprivileged service: %w", err)
	}
	for _, file := range files {
		_ = file.Close()
	}
	_ = httpListener.Close()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	for {
		select {
		case err := <-finished:
			return err
		case received := <-signals:
			_ = command.Process.Signal(received)
		}
	}
}

// DropForMaintenance keeps offline updates on the same data owner as the
// installed supervisor. The root CLI only arranges the credential transition.
func DropForMaintenance(arguments []string, storagePath string) (bool, error) {
	if os.Geteuid() != 0 {
		return false, nil
	}
	stored, found := serviceinstall.StoredOptions()
	if !found || filepath.Clean(stored.StoragePath) != filepath.Clean(storagePath) {
		return false, nil
	}
	account, credentials, err := serviceCredentials()
	if err != nil {
		return true, err
	}
	command := exec.Command(os.Args[0], arguments...)
	command.Dir = storagePath
	command.SysProcAttr = &syscall.SysProcAttr{Credential: credentials}
	command.Env = append(filteredEnvironment(), "HOME="+storagePath, "USER="+account.Username, "LOGNAME="+account.Username)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return true, command.Run()
}

func serviceCredentials() (*user.User, *syscall.Credential, error) {
	account, err := user.Lookup(serviceinstall.ServiceUserName())
	if err != nil {
		return nil, nil, fmt.Errorf("service user is unavailable: %w", err)
	}
	if account.Uid == "0" {
		return nil, nil, fmt.Errorf("service user must not be root")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, nil, err
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return nil, nil, err
	}
	return account, &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}}, nil
}

func filteredEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") && !strings.HasPrefix(entry, "USER=") && !strings.HasPrefix(entry, "LOGNAME=") {
			result = append(result, entry)
		}
	}
	return result
}

func InheritedListeners(httpPort int, tlsEnabled bool) (net.Listener, net.Listener, error) {
	if os.Geteuid() == 0 {
		return nil, nil, fmt.Errorf("service worker must not run as root")
	}
	httpListener, err := inheritedListener(3, httpPort)
	if err != nil {
		return nil, nil, err
	}
	if !tlsEnabled {
		return httpListener, nil, nil
	}
	httpsListener, err := inheritedListener(4, 443)
	if err != nil {
		_ = httpListener.Close()
		return nil, nil, err
	}
	return httpListener, httpsListener, nil
}

func inheritedListener(descriptor uintptr, expectedPort int) (net.Listener, error) {
	file := os.NewFile(descriptor, "sitebrush-listener")
	if file == nil {
		return nil, fmt.Errorf("missing inherited listener %d", descriptor)
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port != expectedPort {
		_ = listener.Close()
		return nil, fmt.Errorf("inherited listener does not own port %d", expectedPort)
	}
	return listener, nil
}
