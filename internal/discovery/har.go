package discovery

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

type HAR struct {
	Log HARLog `json:"log"`
}

func (h *HAR) UnmarshalJSON(data []byte) error {
	type wire HAR
	var parsed wire
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*h = HAR(parsed)
	return nil
}

type HARLog struct {
	Entries []HAREntry `json:"entries"`
}
type HARHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type HAREntry struct {
	StartedDateTime string `json:"startedDateTime"`
	Request         struct {
		Method   string      `json:"method"`
		URL      string      `json:"url"`
		Headers  []HARHeader `json:"headers"`
		PostData struct {
			Text     string `json:"text"`
			MIMEType string `json:"mimeType"`
		} `json:"postData"`
	} `json:"request"`
	Response struct {
		Status  int         `json:"status"`
		Headers []HARHeader `json:"headers"`
		Content struct {
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
			Encoding string `json:"encoding"`
		} `json:"content"`
	} `json:"response"`
	ResourceType string `json:"_resourceType"`
}

func AnalyzeHAR(har HAR, target string) Report {
	capture := &page.Capture{Entries: []page.Exchange{}}
	remaining := page.MaxCaptureTotalBytes
	var first time.Time
	document := page.Document{Provider: page.Provider("har"), Traffic: capture, Type: page.ContentHTML}
	for i, item := range har.Log.Entries {
		if i >= page.MaxCaptureRequests {
			capture.Dropped++
			continue
		}
		entry := page.Exchange{ID: fmt.Sprintf("r%d", i+1), URL: item.Request.URL, Method: item.Request.Method, Status: item.Response.Status, MIMEType: item.Response.Content.MIMEType, ResourceType: item.ResourceType, RequestHeaders: harHeaders(item.Request.Headers), ResponseHeaders: harHeaders(item.Response.Headers)}
		if item.Request.PostData.MIMEType != "" {
			entry.RequestHeaders["content-type"] = item.Request.PostData.MIMEType
		}
		requestBody := item.Request.PostData.Text
		if len(requestBody) > page.MaxCaptureBodyBytes || len(requestBody) > remaining {
			entry.Truncated = true
		} else {
			entry.RequestBody = requestBody
			remaining -= len(requestBody)
		}
		body := item.Response.Content.Text
		if item.Response.Content.Encoding == "base64" {
			if len(body) > base64.StdEncoding.EncodedLen(page.MaxCaptureBodyBytes) {
				entry.Truncated = true
				body = ""
			} else {
				decoded, err := base64.StdEncoding.DecodeString(body)
				if err != nil {
					entry.Error = "HAR body decoding failed"
					body = ""
				} else {
					body = string(decoded)
				}
			}
		} else if item.Response.Content.Encoding != "" {
			entry.Error = "unsupported HAR body encoding"
			body = ""
		}
		if len(body) > page.MaxCaptureBodyBytes || len(body) > remaining {
			entry.Truncated = true
		} else {
			entry.ResponseBody = body
			remaining -= len(body)
		}
		if timestamp, err := time.Parse(time.RFC3339Nano, item.StartedDateTime); err == nil {
			if first.IsZero() {
				first = timestamp
			}
			entry.StartedMS = float64(timestamp.Sub(first).Microseconds()) / 1000
		}
		if entry.ResourceType == "" && strings.Contains(entry.MIMEType, "html") {
			entry.ResourceType = "Document"
		}
		if target == "" {
			target = entry.URL
		}
		if document.FinalURL == "" && strings.EqualFold(entry.ResourceType, "document") {
			document.FinalURL = entry.URL
			document.StatusCode = entry.Status
			document.HTML = entry.ResponseBody
		}
		capture.Entries = append(capture.Entries, entry)
	}
	if document.FinalURL == "" {
		document.FinalURL = target
	}
	return Analyze(target, document)
}

func harHeaders(headers []HARHeader) map[string]string {
	result := map[string]string{}
	for _, header := range headers {
		result[strings.ToLower(header.Name)] = header.Value
	}
	return result
}
