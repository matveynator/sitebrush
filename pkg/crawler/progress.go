package crawler

// ProgressEvent is emitted by page and site import crawlers.
type ProgressEvent struct {
	Token                  string            `json:"token"`
	Stage                  string            `json:"stage"`
	FoundTotal             int               `json:"found_total"`
	DownloadedTotal        int               `json:"downloaded_total"`
	DownloadTotal          int               `json:"download_total,omitempty"`
	DownloadedBytes        int64             `json:"downloaded_bytes,omitempty"`
	DownloadTotalBytes     int64             `json:"download_total_bytes,omitempty"`
	FailedTotal            int               `json:"failed_total"`
	FailedURLs             []string          `json:"failed_urls,omitempty"`
	FailedReasons          map[string]string `json:"failed_reasons,omitempty"`
	RetryAttempt           int               `json:"retry_attempt,omitempty"`
	RetryTotal             int               `json:"retry_total,omitempty"`
	RetryDelaySeconds      int               `json:"retry_delay_seconds,omitempty"`
	CurrentURL             string            `json:"current_url"`
	CurrentError           string            `json:"current_error,omitempty"`
	CurrentPercent         int               `json:"current_percent"`
	CurrentDownloadedBytes int64             `json:"current_downloaded_bytes"`
	CurrentSizeBytes       int64             `json:"current_size_bytes"`
	CompletedPercent       int               `json:"completed_percent"`
	Message                string            `json:"message"`
}

type progressTrackerRequest struct {
	action string
	token  string
	stream chan ProgressEvent
	event  ProgressEvent
}

// ProgressTracker routes progress events by token using a channel actor.
type ProgressTracker struct {
	requests chan progressTrackerRequest
}

func NewProgressTracker() *ProgressTracker {
	tracker := &ProgressTracker{requests: make(chan progressTrackerRequest)}
	go tracker.loop()
	return tracker
}

func (tracker *ProgressTracker) Subscribe(token string) chan ProgressEvent {
	stream := make(chan ProgressEvent, 32)
	tracker.requests <- progressTrackerRequest{action: "subscribe", token: token, stream: stream}
	return stream
}

func (tracker *ProgressTracker) Unsubscribe(token string, stream chan ProgressEvent) {
	tracker.requests <- progressTrackerRequest{action: "unsubscribe", token: token, stream: stream}
}

func (tracker *ProgressTracker) Publish(event ProgressEvent) {
	tracker.requests <- progressTrackerRequest{action: "publish", token: event.Token, event: event}
}

func (tracker *ProgressTracker) loop() {
	subscribersByToken := make(map[string]map[chan ProgressEvent]struct{})
	for request := range tracker.requests {
		switch request.action {
		case "subscribe":
			if _, exists := subscribersByToken[request.token]; !exists {
				subscribersByToken[request.token] = make(map[chan ProgressEvent]struct{})
			}
			subscribersByToken[request.token][request.stream] = struct{}{}
		case "unsubscribe":
			group := subscribersByToken[request.token]
			delete(group, request.stream)
			close(request.stream)
		case "publish":
			for stream := range subscribersByToken[request.token] {
				select {
				case stream <- request.event:
				default:
				}
			}
		}
	}
}
