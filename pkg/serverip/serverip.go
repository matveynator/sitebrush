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

// Start owns configuration, persistence ordering, and interface discovery. Each
// reply contains a copy; callers never share the owner's mutable slices.
func Start(settings Settings, save func(Settings) error, stop <-chan struct{}) *Process {
	process := &Process{requests: make(chan Request), done: make(chan struct{})}
	requests := process.requests
	go func() {
		defer close(process.done)
		addresses, failures := Enumerate()
		observed := []string{}
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		snapshot := func() Snapshot {
			result := Snapshot{Addresses: append([]Address(nil), addresses...), Settings: Settings{Manual: append([]string(nil), settings.Manual...), Preferred: settings.Preferred}, Errors: append([]string(nil), failures...)}
			for _, ip := range settings.Manual {
				result.Addresses = append(result.Addresses, Address{IP: ip, Active: true, Public: true, Source: "manual"})
			}
			for _, ip := range observed {
				result.Addresses = append(result.Addresses, Address{IP: ip, Active: true, Public: true, Source: "outgoing"})
			}
			return result
		}
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				addresses, failures = Enumerate()
			case request := <-requests:
				select {
				case <-request.Stop:
					continue
				default:
				}
				var err error
				switch request.Action {
				case "save":
					var normalized Settings
					normalized, err = Normalize(request.Settings)
					if err == nil {
						proposed := snapshot()
						proposed.Settings = normalized
						proposed.Addresses = slices.DeleteFunc(proposed.Addresses, func(address Address) bool { return address.Source == "manual" })
						for _, ip := range normalized.Manual {
							proposed.Addresses = append(proposed.Addresses, Address{IP: ip, Active: true, Public: true, Source: "manual"})
						}
						if normalized.Preferred != "" && proposed.Primary(nil) == "" {
							err = errors.New("preferred IP is not an active public address or a manual address")
						}
						if err == nil && save != nil {
							err = save(normalized)
						}
						if err == nil {
							settings = normalized
						}
					}
				case "refresh":
					addresses, failures = Enumerate()
				case "observed":
					normalized, normalizeErr := Normalize(Settings{Manual: request.Observed})
					err = normalizeErr
					if err == nil {
						observed = normalized.Manual
					}
				}
				if request.Reply != nil {
					select {
					case request.Reply <- Result{Snapshot: snapshot(), Err: err}:
					case <-request.Stop:
					case <-stop:
						return
					}
				}
			}
		}
	}()
	return process
}

func Exchange(process *Process, request Request, stop <-chan struct{}) (Snapshot, error) {
	if process == nil {
		return Snapshot{}, errors.New("server IP inventory is unavailable")
	}
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
	case <-process.done:
		return Snapshot{}, errors.New("server IP owner stopped")
	case result := <-request.Reply:
		return result.Snapshot, result.Err
	case <-stop:
		return Snapshot{}, errors.New("server IP response canceled")
	}
}
