package discovery

import (
	"github.com/simonbalfe/pagelode/orchestrator"
	"github.com/simonbalfe/pagelode/page"
)

// Report is the result of a discovery run.
type Report struct {
	Page        PageInfo               `json:"page"`
	Outcome     string                 `json:"outcome"`
	Summary     Summary                `json:"summary"`
	Endpoints   []Endpoint             `json:"endpoints"`
	Evidence    []Evidence             `json:"evidence,omitempty"`
	Protocols   []Signal               `json:"protocols"`
	Auth        []Signal               `json:"auth"`
	Protections []Signal               `json:"protections"`
	Sequences   []Sequence             `json:"sequences,omitempty"`
	Pagination  []Signal               `json:"pagination"`
	DataSources []DataSource           `json:"dataSources"`
	Warnings    []string               `json:"warnings"`
	Attempts    []orchestrator.Attempt `json:"attempts,omitempty"`
}

// PageInfo identifies the analyzed page and the provider that loaded it.
type PageInfo struct {
	URL      string        `json:"url"`
	FinalURL string        `json:"finalUrl"`
	Title    string        `json:"title"`
	Provider page.Provider `json:"provider"`
	Status   int           `json:"status"`
}

// Summary counts the requests seen and how many were classified, dropped, truncated, or incomplete.
type Summary struct {
	Requests      int   `json:"requests"`
	DataRequests  int   `json:"dataRequests"`
	NoiseRequests int   `json:"noiseRequests"`
	EndpointCount int   `json:"endpointCount"`
	Dropped       int   `json:"dropped"`
	DurationMS    int64 `json:"durationMs"`
	Incomplete    int   `json:"incomplete"`
	Truncated     int   `json:"truncated"`
}

// Endpoint groups observed requests sharing host, method, and normalized path, with schemas merged across calls.
// Replayability is always "untested" because discovery never replays requests.
type Endpoint struct {
	ID             string   `json:"id"`
	Host           string   `json:"host"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	Protocol       string   `json:"protocol"`
	Operation      string   `json:"operation,omitempty"`
	Operations     []string `json:"operations,omitempty"`
	Candidate      string   `json:"candidateOperation"`
	Calls          int      `json:"calls"`
	Statuses       []int    `json:"statuses"`
	Parameters     []Field  `json:"parameters"`
	RequestSchema  []Field  `json:"requestSchema"`
	ResponseSchema []Field  `json:"responseSchema"`
	Auth           []string `json:"auth"`
	Evidence       []string `json:"evidence,omitempty"`
	Replayability  string   `json:"replayability"`
}

// Field is one inferred parameter or JSON schema path and the types observed for it.
type Field struct {
	Path     string   `json:"path"`
	Types    []string `json:"types"`
	Evidence []string `json:"evidence,omitempty"`
}

// Evidence records how one captured request was classified; its ID is referenced by endpoints and signals.
type Evidence struct {
	ID             string  `json:"id"`
	URL            string  `json:"url"`
	Method         string  `json:"method"`
	Status         int     `json:"status"`
	ResourceType   string  `json:"resourceType"`
	Classification string  `json:"classification"`
	Score          int     `json:"score"`
	Reason         string  `json:"reason"`
	FrameID        string  `json:"frameId,omitempty"`
	Initiator      string  `json:"initiator,omitempty"`
	StartedMS      float64 `json:"startedMs"`
	Truncated      bool    `json:"truncated"`
	Incomplete     bool    `json:"incomplete"`
}

// Signal is a detected protocol, auth, protection, or pagination trait; Basis says whether it was observed or inferred.
type Signal struct {
	EndpointID string   `json:"endpointId,omitempty"`
	Kind       string   `json:"kind"`
	Location   string   `json:"location,omitempty"`
	Name       string   `json:"name,omitempty"`
	Basis      string   `json:"basis"`
	Evidence   []string `json:"evidence,omitempty"`
}

// Sequence places a request for an endpoint on the capture timeline.
type Sequence struct {
	EvidenceID string  `json:"evidenceId"`
	EndpointID string  `json:"endpointId"`
	StartedMS  float64 `json:"startedMs"`
}

// DataSource points at a candidate location of page data, either in an endpoint response or embedded JSON.
type DataSource struct {
	EndpointID string   `json:"endpointId,omitempty"`
	Kind       string   `json:"kind"`
	Location   string   `json:"location"`
	Basis      string   `json:"basis"`
	Evidence   []string `json:"evidence,omitempty"`
}
