package updateannounce

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Selection struct {
	Language string
	Servers  []Status
	Waiting  bool
}

func (hub *Hub) Select(installations []Installation, target, email string, stop <-chan struct{}) (Selection, error) {
	selection := Selection{Language: "en"}
	var languageAt time.Time
	consumer := make(chan struct{})
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-stop:
		case <-timer.C:
		case <-finished:
			return
		}
		close(consumer)
	}()
	for _, installation := range installations {
		select {
		case <-consumer:
			return selection, errors.New("announcement preparation canceled")
		default:
		}
		key, err := DecodeKey(installation.Key)
		if err != nil {
			return selection, err
		}
		status, err := hub.Check(installation.ID, key, consumer)
		if err != nil {
			selection.Waiting = true
			continue
		}
		owner, eligible, err := Eligible(status, target, email)
		if err != nil {
			selection.Waiting = true
			continue
		}
		if !eligible {
			continue
		}
		address, _ := url.Parse(status.PanelURL)
		if !strings.EqualFold(address.Hostname(), strings.TrimSuffix(installation.Domain, ".")) {
			selection.Waiting = true
			continue
		}
		if len(selection.Servers) == 0 || owner.LanguageAt.After(languageAt) {
			selection.Language = owner.Language
			languageAt = owner.LanguageAt
		}
		selection.Servers = append(selection.Servers, status)
	}
	select {
	case <-consumer:
		return selection, errors.New("announcement preparation timed out or its consumer stopped")
	default:
		return selection, nil
	}
}

// Eligibility is based on a live proof. Unknown versions and busy installations
// are deferred rather than being mistaken for an outdated installation.
func Eligible(status Status, target, email string) (Owner, bool, error) {
	if !status.Enabled {
		return Owner{}, false, nil
	}
	if status.Busy {
		return Owner{}, false, errors.New("installation is changing versions")
	}
	installed, err := strconv.ParseUint(status.Version, 10, 64)
	if err != nil {
		return Owner{}, false, errors.New("installed version is unknown")
	}
	available, err := strconv.ParseUint(target, 10, 64)
	if err != nil {
		return Owner{}, false, err
	}
	if installed >= available {
		return Owner{}, false, nil
	}
	if err := ValidatePanelURL(status.PanelURL); err != nil {
		return Owner{}, false, err
	}
	for _, owner := range status.Owners {
		if strings.EqualFold(strings.TrimSpace(owner.Email), email) {
			return owner, true, nil
		}
	}
	return Owner{}, false, nil
}

func ValidatePanelURL(raw string) error {
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "https" || address.User != nil || address.Fragment != "" || address.Path != "/" || address.RawQuery != "server_update" || address.Opaque != "" {
		return errors.New("invalid installation update URL")
	}
	host := address.Hostname()
	if host == "" || !strings.Contains(host, ".") || strings.ContainsAny(host, " \\/@?#\r\n") || net.ParseIP(host) != nil || strings.HasSuffix(host, ".localhost") {
		return errors.New("a public installation domain is required")
	}
	if port := address.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("invalid installation HTTPS port")
		}
	}
	return nil
}
