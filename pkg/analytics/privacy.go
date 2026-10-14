package analytics

import (
	"encoding/json"
	"strings"
)

// Development checkpoints may predate the structural-only form protocol.
// The SQL owner removes their obsolete fields on read, preserving aggregate
// counts and identifiers rather than resetting a site's analytics history.
func sanitizeBrowserCheckpoint(encoded string) (string, error) {
	if !strings.Contains(encoded, `"Values"`) && !strings.Contains(encoded, `"Fingerprint"`) && !strings.Contains(encoded, `"Client"`) {
		return encoded, nil
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	var checkpoint any
	if err := decoder.Decode(&checkpoint); err != nil {
		return "", err
	}
	removeLegacyBrowserFields(checkpoint)
	cleaned, err := json.Marshal(checkpoint)
	return string(cleaned), err
}

func removeLegacyBrowserFields(checkpoint any) {
	switch container := checkpoint.(type) {
	case map[string]any:
		for name, nested := range container {
			switch name {
			case "Values", "Fingerprint", "Client":
				delete(container, name)
			default:
				removeLegacyBrowserFields(nested)
			}
		}
	case []any:
		for _, nested := range container {
			removeLegacyBrowserFields(nested)
		}
	}
}
