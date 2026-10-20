package page

// CaptureOptions configures a browser render that records network traffic.
type CaptureOptions struct {
	// WaitMS is how long, in milliseconds, to keep recording after the page loads.
	WaitMS int `json:"wait_ms"`
	// Profile names a persistent browser profile to render with; empty means none.
	Profile string `json:"-"`
}

// ExploreOptions configures interactive exploration of a rendered page.
type ExploreOptions struct {
	// MaxClicks caps how many elements exploration may click.
	MaxClicks int
}

// Capture limits bound the size of a recorded Capture: the number of
// exchanges, the bytes kept per body, and the bytes kept across all bodies.
const (
	MaxCaptureRequests   = 200
	MaxCaptureBodyBytes  = 256 << 10
	MaxCaptureTotalBytes = 2 << 20
)

// Exchange is one recorded network request and its response.
// Truncated reports that a body was cut to fit the capture limits.
type Exchange struct {
	ID              string            `json:"id"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	ResourceType    string            `json:"resource_type"`
	FrameID         string            `json:"frame_id,omitempty"`
	Initiator       string            `json:"initiator,omitempty"`
	StartedMS       float64           `json:"started_ms"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	Status          int               `json:"status"`
	MIMEType        string            `json:"mime_type,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	Truncated       bool              `json:"truncated,omitempty"`
	Error           string            `json:"error,omitempty"`
}

// Capture is the network traffic recorded for a page, from a browser render or an imported HAR.
// Dropped counts exchanges omitted because MaxCaptureRequests was reached.
type Capture struct {
	Entries    []Exchange `json:"entries"`
	Dropped    int        `json:"dropped"`
	DurationMS int64      `json:"duration_ms"`
}
