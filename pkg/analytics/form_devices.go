package analytics

import "time"

// Device comes from the original observation. Historical attempts keep an
// unknown device instead of borrowing the visitor's latest browser category.
func mergeFormObservation(merged FormObservation, observation *FormObservation, now time.Time) FormObservation {
	merged.Device = observation.Device
	merged.Viewed = merged.Viewed || observation.Viewed
	merged.Started = merged.Started || observation.Started && !observation.StartedAt.After(now)
	merged.Submitted = merged.Submitted || observation.Submitted && !observation.SubmittedAt.After(now)
	merged.Completed = merged.Completed || observation.Completed && !observation.CompletedAt.After(now)
	merged.Failed = merged.Failed || observation.Failed && !observation.FailedAt.After(now)
	if observation.Last.After(merged.Last) {
		merged.Last = observation.Last
		merged.LastField = observation.LastField
	}
	return merged
}

func addFormDevice(aggregate *ConversionFormDevice, observation FormObservation, now time.Time) {
	if observation.Viewed {
		aggregate.Viewed++
	}
	if observation.Started {
		aggregate.Started++
	}
	if observation.Submitted {
		aggregate.Submitted++
	}
	if observation.Completed {
		aggregate.Completed++
	}
	if observation.Failed {
		aggregate.Failed++
	}
	if observation.Started && !observation.Completed && now.Sub(observation.Last) > 30*time.Minute {
		aggregate.Abandoned++
	}
}
