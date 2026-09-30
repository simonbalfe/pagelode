package chromefetch

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/simonbalfe/pagelode/internal/page"
)

type capturedRequest struct {
	entry     page.Exchange
	requestID network.RequestID
	complete  bool
	bytes     int64
}

type collector struct {
	mu            sync.Mutex
	started       time.Time
	entries       []*capturedRequest
	current       map[network.RequestID]*capturedRequest
	dropped       int
	retainedBytes int
}

func newCollector() *collector {
	return &collector{started: time.Now(), current: make(map[network.RequestID]*capturedRequest)}
}

func (c *collector) listen(event any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e := event.(type) {
	case *network.EventRequestWillBeSent:
		c.requestStarted(e)
	case *network.EventRequestWillBeSentExtraInfo:
		if request := c.current[e.RequestID]; request != nil {
			for name, value := range stringHeaders(e.Headers) {
				request.entry.RequestHeaders[name] = value
			}
		}
	case *network.EventResponseReceived:
		if request := c.current[e.RequestID]; request != nil {
			request.entry.Status = int(e.Response.Status)
			request.entry.MIMEType = e.Response.MimeType
			request.entry.ResponseHeaders = stringHeaders(e.Response.Headers)
		}
	case *network.EventDataReceived:
		if request := c.current[e.RequestID]; request != nil {
			request.bytes += e.DataLength
		}
	case *network.EventLoadingFinished:
		if request := c.current[e.RequestID]; request != nil {
			request.complete = true
		}
	case *network.EventLoadingFailed:
		if request := c.current[e.RequestID]; request != nil {
			request.entry.Error = "network request failed"
		}
	case *network.EventWebSocketCreated:
		if len(c.entries) >= page.MaxCaptureRequests {
			c.dropped++
			return
		}
		c.entries = append(c.entries, &capturedRequest{entry: page.Exchange{ID: fmt.Sprintf("r%d", len(c.entries)+1), URL: e.URL, Method: "GET", ResourceType: "WebSocket", StartedMS: float64(time.Since(c.started).Microseconds()) / 1000}})
	}
}

func (c *collector) requestStarted(e *network.EventRequestWillBeSent) {
	if e.RedirectResponse != nil {
		if previous := c.current[e.RequestID]; previous != nil {
			previous.entry.Status = int(e.RedirectResponse.Status)
			previous.entry.ResponseHeaders = stringHeaders(e.RedirectResponse.Headers)
			previous.entry.MIMEType = e.RedirectResponse.MimeType
			previous.entry.Error = "redirect response body not retained"
		}
	}
	if len(c.entries) >= page.MaxCaptureRequests {
		c.dropped++
		delete(c.current, e.RequestID)
		return
	}
	entry := page.Exchange{ID: fmt.Sprintf("r%d", len(c.entries)+1), URL: e.Request.URL, Method: e.Request.Method, ResourceType: string(e.Type), FrameID: string(e.FrameID), StartedMS: float64(time.Since(c.started).Microseconds()) / 1000, RequestHeaders: stringHeaders(e.Request.Headers)}
	if e.Initiator != nil {
		entry.Initiator = string(e.Initiator.Type)
	}
	for _, part := range e.Request.PostDataEntries {
		if len(part.Bytes) > page.MaxCaptureBodyBytes*2 {
			entry.Truncated = true
			break
		}
		decoded, err := base64.StdEncoding.DecodeString(part.Bytes)
		if err != nil {
			entry.Error = "request body decoding failed"
			continue
		}
		if len(entry.RequestBody)+len(decoded) > page.MaxCaptureBodyBytes || c.retainedBytes+len(entry.RequestBody)+len(decoded) > page.MaxCaptureTotalBytes {
			entry.Truncated = true
			break
		}
		entry.RequestBody += string(decoded)
	}
	if e.Request.HasPostData && len(e.Request.PostDataEntries) == 0 {
		entry.Error = "request body unavailable"
	}
	c.retainedBytes += len(entry.RequestBody)
	request := &capturedRequest{entry: entry, requestID: e.RequestID}
	c.entries = append(c.entries, request)
	c.current[e.RequestID] = request
}

func stringHeaders(headers network.Headers) map[string]string {
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		result[strings.ToLower(name)] = fmt.Sprint(value)
	}
	return result
}

func (c *collector) snapshot(ctx context.Context) *page.Capture {
	c.mu.Lock()
	requests := make([]capturedRequest, len(c.entries))
	for i, request := range c.entries {
		requests[i] = *request
		requests[i].entry.RequestHeaders = maps.Clone(request.entry.RequestHeaders)
		requests[i].entry.ResponseHeaders = maps.Clone(request.entry.ResponseHeaders)
	}
	dropped := c.dropped
	c.mu.Unlock()
	result := &page.Capture{Entries: make([]page.Exchange, 0, len(requests)), Dropped: dropped, DurationMS: time.Since(c.started).Milliseconds()}
	remaining := page.MaxCaptureTotalBytes
	for _, request := range requests {
		entry := request.entry
		if len(entry.RequestBody) > remaining {
			entry.RequestBody = ""
			entry.Truncated = true
		} else {
			remaining -= len(entry.RequestBody)
		}
		if request.bytes > page.MaxCaptureBodyBytes {
			entry.Truncated = true
		}
		if request.complete && bodyUseful(entry) && !entry.Truncated && remaining > 0 && entry.Status != 204 {
			var body []byte
			err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				body, err = network.GetResponseBody(request.requestID).Do(ctx)
				return err
			}))
			if err != nil {
				entry.Error = "response body unavailable"
			} else if len(body) > page.MaxCaptureBodyBytes || len(body) > remaining {
				entry.Truncated = true
			} else {
				entry.ResponseBody = string(body)
				remaining -= len(body)
			}
		} else if bodyUseful(entry) && !request.complete && entry.Error == "" {
			entry.Error = "request incomplete at end of observation"
		} else if bodyUseful(entry) && remaining == 0 {
			entry.Truncated = true
		}
		result.Entries = append(result.Entries, entry)
	}
	return result
}

func bodyUseful(entry page.Exchange) bool {
	kind := strings.ToLower(entry.ResourceType)
	mime := strings.ToLower(entry.MIMEType)
	return kind == "fetch" || kind == "xhr" || kind == "document" || strings.Contains(mime, "json") || strings.Contains(mime, "html")
}
