package hostingandsupport

import (
	"strings"
	"time"
)

// PublicTrialGroups separates temporary sessions that still require cleanup
// from successfully handed-off sites, whose content must not be removed.
type PublicTrialGroups struct {
	Active         []PublicTrialSite
	CleanupPending []PublicTrialSite
	History        []PublicTrialSite
}

// GroupPublicTrialSites is a pure read-model operation. An expired trial
// remains visible as cleanup pending until the durable deletion completes;
// a registered or activated site is historical, not temporary or deletable.
func GroupPublicTrialSites(sites []PublicTrialSite, now time.Time) PublicTrialGroups {
	groups := PublicTrialGroups{}
	for _, site := range sites {
		switch strings.ToLower(strings.TrimSpace(site.Status)) {
		case "active", "private", "email_pending", "email_verified", "expiring":
			deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(site.DeleteAfter))
			if err != nil || !now.Before(deadline) {
				groups.CleanupPending = append(groups.CleanupPending, site)
			} else {
				groups.Active = append(groups.Active, site)
			}
		default:
			// A completed registration (registered/activated/importing) is
			// not an abandoned temporary site even if it has an old deadline.
			// The cleanup worker must never delete it based on that deadline.
			groups.History = append(groups.History, site)
		}
	}
	return groups
}
