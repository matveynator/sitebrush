// Package serverip owns the installation's web address inventory.
package serverip

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/outboundhttp"
)

type Settings struct {
	Manual    []string `json:"manual"`
	Preferred string   `json:"preferred"`
}
type Address struct {
	IP        string `json:"ip"`
	Interface string `json:"interface,omitempty"`
	Active    bool   `json:"active"`
	Public    bool   `json:"public"`
	Source    string `json:"source"`
}
type Snapshot struct {
	Addresses []Address `json:"addresses"`
	Settings  Settings  `json:"settings"`
	Errors    []string  `json:"errors"`
}

func Normalize(settings Settings) (Settings, error) {
	normalized := Settings{}
	if len(settings.Manual) > 64 {
		return normalized, errors.New("at most 64 manual IP addresses are allowed")
	}
	for _, text := range settings.Manual {
		address, err := netip.ParseAddr(strings.TrimSpace(text))
		if err != nil || address.Zone() != "" || !outboundhttp.IPAllowed(net.ParseIP(address.Unmap().String())) {
			return Settings{}, fmt.Errorf("invalid public IP: %s", text)
		}
		canonical := address.Unmap().String()
		if !slices.Contains(normalized.Manual, canonical) {
			normalized.Manual = append(normalized.Manual, canonical)
		}
	}
	if settings.Preferred != "" {
		preferred, err := Normalize(Settings{Manual: []string{settings.Preferred}})
		if err != nil {
			return Settings{}, err
		}
		normalized.Preferred = preferred.Manual[0]
	}
	slices.Sort(normalized.Manual)
	return normalized, nil
}

// Enumerate uses Go's native interface implementations on every supported OS.
// A failing interface does not discard addresses returned by other interfaces.
func Enumerate() ([]Address, []string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, []string{err.Error()}
	}
	return enumerateInterfaces(interfaces, func(networkInterface net.Interface) ([]net.Addr, error) { return networkInterface.Addrs() })
}

func enumerateInterfaces(interfaces []net.Interface, read func(net.Interface) ([]net.Addr, error)) ([]Address, []string) {
	addresses := []Address{}
	failures := []string{}
	for _, networkInterface := range interfaces {
		interfaceAddresses, addressErr := read(networkInterface)
		if addressErr != nil {
			failures = append(failures, networkInterface.Name+": "+addressErr.Error())
			continue
		}
		for _, interfaceAddress := range interfaceAddresses {
			var ip net.IP
			switch parsed := interfaceAddress.(type) {
			case *net.IPNet:
				ip = parsed.IP
			case *net.IPAddr:
				ip = parsed.IP
			}
			if ip == nil {
				continue
			}
			addresses = append(addresses, Address{IP: ip.String(), Interface: networkInterface.Name, Active: networkInterface.Flags&net.FlagUp != 0, Public: outboundhttp.IPAllowed(ip), Source: "interface"})
		}
	}
	slices.SortFunc(addresses, func(first, second Address) int {
		return strings.Compare(first.IP+first.Interface, second.IP+second.Interface)
	})
	return addresses, failures
}

func (snapshot Snapshot) Candidates() []net.IP {
	candidates := []net.IP{}
	for _, address := range snapshot.Addresses {
		if !address.Active || !address.Public {
			continue
		}
		candidate := net.ParseIP(address.IP)
		if !outboundhttp.IPAllowed(candidate) {
			continue
		}
		duplicate := false
		for _, previous := range candidates {
			if previous.Equal(candidate) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			candidates = append(candidates, candidate)
		}
	}
	slices.SortFunc(candidates, func(first, second net.IP) int { return strings.Compare(first.String(), second.String()) })
	return candidates
}

func (snapshot Snapshot) Primary(domainIPs []net.IP) string {
	candidates := snapshot.Candidates()
	if snapshot.Settings.Preferred != "" {
		for _, candidate := range candidates {
			if candidate.String() == snapshot.Settings.Preferred {
				return candidate.String()
			}
		}
		return ""
	}
	for _, domainIP := range domainIPs {
		for _, candidate := range candidates {
			if candidate.Equal(domainIP) {
				return candidate.String()
			}
		}
	}
	for _, candidate := range candidates {
		if candidate.To4() != nil {
			return candidate.String()
		}
	}
	if len(candidates) > 0 {
		return candidates[0].String()
	}
	return ""
}

type Process struct {
	requests chan Request
	done     chan struct{}
}

func (process *Process) Done() <-chan struct{} { return process.done }

type Request struct {
	Action   string
	Settings Settings
	Observed []string
	Reply    chan Result
	Stop     <-chan struct{}
}
type Result struct {
	Snapshot Snapshot
	Err      error
}

// Each worker receives ownership of a job and returns it through the owner's
// result channel. Filesystem and database latency never occupy the select loop.
type scanResult struct {
	request   Request
	addresses []Address
	failures  []string
	err       error
}
type saveResult struct {
	request  Request
	settings Settings
	err      error
}

func Start(settings Settings, save func(Settings, <-chan struct{}) error, stop <-chan struct{}) *Process {
	return start(settings, save, Enumerate, stop)
}

func start(settings Settings, save func(Settings, <-chan struct{}) error, scan func() ([]Address, []string), stop <-chan struct{}) *Process {
	process := &Process{requests: make(chan Request), done: make(chan struct{})}
	initialSettings := copySettings(settings)
	go run(process, initialSettings, save, scan, stop)
	return process
}

func run(process *Process, settings Settings, save func(Settings, <-chan struct{}) error, scan func() ([]Address, []string), stop <-chan struct{}) {
	defer close(process.done)
	scans := make(chan scanResult)
	saves := make(chan saveResult)
	scanBusy, saveBusy := true, false
	go scanInterfaces(scan, Request{}, scans, stop)
	addresses := []Address{}
	failures := []string{"interface discovery pending"}
	observed := []string{}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	snapshot := func() Snapshot { return inventorySnapshot(settings, addresses, failures, observed) }
	for {
		select {
		case <-stop:
			return
		case result := <-scans:
			scanBusy = false
			if result.err == nil {
				addresses, failures = result.addresses, result.failures
			}
			reply(result.request, Result{Snapshot: snapshot(), Err: result.err}, stop)
		case result := <-saves:
			saveBusy = false
			if result.err == nil {
				settings = result.settings
			}
			reply(result.request, Result{Snapshot: snapshot(), Err: result.err}, stop)
		case <-ticker.C:
			if !scanBusy {
				scanBusy = true
				go scanInterfaces(scan, Request{}, scans, stop)
			}
		case request := <-process.requests:
			if sessionError(request.Stop, stop) != nil {
				continue
			}
			var err error
			switch request.Action {
			case "save":
				if saveBusy {
					err = errors.New("public IP settings are already being saved")
					break
				}
				var normalized Settings
				normalized, err = Normalize(request.Settings)
				if err == nil {
					proposed := inventorySnapshot(normalized, addresses, failures, observed)
					if normalized.Preferred != "" && proposed.Primary(nil) == "" {
						err = errors.New("preferred IP is not an active public address or a manual address")
					}
				}
				if err == nil {
					saveBusy = true
					go saveSettings(save, normalized, request, saves, stop)
					continue
				}
			case "refresh":
				if scanBusy {
					err = errors.New("interface discovery is already running")
					break
				}
				scanBusy = true
				go scanInterfaces(scan, request, scans, stop)
				continue
			case "observed":
				var normalized Settings
				normalized, err = Normalize(Settings{Manual: request.Observed})
				if err == nil {
					observed = normalized.Manual
				}
			}
			reply(request, Result{Snapshot: snapshot(), Err: err}, stop)
		}
	}
}

func inventorySnapshot(settings Settings, addresses []Address, failures, observed []string) Snapshot {
	result := Snapshot{Addresses: append([]Address(nil), addresses...), Settings: copySettings(settings), Errors: append([]string(nil), failures...)}
	for _, ip := range settings.Manual {
		result.Addresses = append(result.Addresses, Address{IP: ip, Active: true, Public: true, Source: "manual"})
	}
	for _, ip := range observed {
		result.Addresses = append(result.Addresses, Address{IP: ip, Active: true, Public: true, Source: "outgoing"})
	}
	return result
}

func copySettings(settings Settings) Settings {
	return Settings{Manual: append([]string(nil), settings.Manual...), Preferred: settings.Preferred}
}

func sessionError(consumer, owner <-chan struct{}) error {
	select {
	case <-consumer:
		return errors.New("server IP consumer stopped")
	case <-owner:
		return errors.New("server IP owner stopped")
	default:
		return nil
	}
}

func scanInterfaces(scan func() ([]Address, []string), request Request, results chan<- scanResult, stop <-chan struct{}) {
	result := scanResult{request: request, err: sessionError(request.Stop, stop)}
	if result.err == nil {
		result.addresses, result.failures = scan()
	}
	// Completion releases the bounded worker slot even after a consumer leaves.
	select {
	case results <- result:
	case <-stop:
	}
}

func saveSettings(save func(Settings, <-chan struct{}) error, settings Settings, request Request, results chan<- saveResult, stop <-chan struct{}) {
	session := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(session)
		select {
		case <-stop:
		case <-request.Stop:
		case <-finished:
		}
	}()
	err := sessionError(request.Stop, stop)
	if err == nil && save != nil {
		err = save(copySettings(settings), session)
	}
	close(finished)
	// A committed write remains authoritative even after its consumer disconnects.
	select {
	case results <- saveResult{request, settings, err}:
	case <-stop:
	}
}

func reply(request Request, result Result, stop <-chan struct{}) {
	if request.Reply == nil || sessionError(request.Stop, stop) != nil {
		return
	}
	select {
	case request.Reply <- result:
	case <-request.Stop:
	case <-stop:
	}
}

func Exchange(process *Process, request Request, stop <-chan struct{}) (Snapshot, error) {
	if process == nil {
		return Snapshot{}, errors.New("server IP inventory is unavailable")
	}
	if err := sessionError(stop, process.done); err != nil {
		return Snapshot{}, err
	}
	request.Settings = copySettings(request.Settings)
	request.Observed = append([]string(nil), request.Observed...)
	request.Reply = make(chan Result, 1)
	request.Stop = stop
	select {
	case process.requests <- request:
	case <-process.done:
		return Snapshot{}, errors.New("server IP owner stopped")
	case <-stop:
		return Snapshot{}, errors.New("server IP request canceled")
	}
	select {
	case result := <-request.Reply:
		return result.Snapshot, result.Err
	case <-process.done:
		return Snapshot{}, errors.New("server IP owner stopped")
	case <-stop:
		return Snapshot{}, errors.New("server IP response canceled")
	}
}
