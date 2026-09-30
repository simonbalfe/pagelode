package page

type CaptureOptions struct {
	WaitMS           int    `json:"wait_ms"`
	ProfileDirectory string `json:"-"`
}

const (
	MaxCaptureRequests   = 200
	MaxCaptureBodyBytes  = 256 << 10
	MaxCaptureTotalBytes = 2 << 20
)

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

type Capture struct {
	Entries    []Exchange `json:"entries"`
	Dropped    int        `json:"dropped"`
	DurationMS int64      `json:"duration_ms"`
}
