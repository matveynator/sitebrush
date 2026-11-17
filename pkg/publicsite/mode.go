package publicsite

import "fmt"

// StaticMode keeps each optimization independently reversible without changing
// the authorization protocol or the public listener.
type StaticMode string

const (
	StaticLegacy   StaticMode = "legacy"
	StaticHTTP10   StaticMode = "http10"
	StaticRoots    StaticMode = "roots"
	StaticHTML     StaticMode = "html"
	StaticSnapshot StaticMode = "snapshot"
)

func ParseStaticMode(name string) (StaticMode, error) {
	mode := StaticMode(name)
	switch mode {
	case StaticLegacy, StaticHTTP10, StaticRoots, StaticHTML, StaticSnapshot:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid public static mode %q: use legacy, http10, roots, html, or snapshot", name)
	}
}

func (mode StaticMode) Enabled(stage StaticMode) bool {
	return mode.rank() >= stage.rank()
}

func (mode StaticMode) String() string {
	if mode == "" {
		return string(StaticSnapshot)
	}
	return string(mode)
}

func (mode StaticMode) rank() int {
	switch mode {
	case StaticLegacy:
		return 0
	case StaticHTTP10:
		return 1
	case StaticRoots:
		return 2
	case StaticHTML:
		return 3
	default:
		return 4
	}
}
