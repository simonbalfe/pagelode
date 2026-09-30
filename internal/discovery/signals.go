package discovery

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/internal/page"
)

func detectProtocol(entry page.Exchange) (string, string) {
	if operations := graphqlBatch(entry.RequestBody); len(operations) > 0 {
		return "graphql_batch", strings.Join(operations, ",")
	}
	parsed, err := url.Parse(entry.URL)
	if err != nil {
		return "http", ""
	}
	path := strings.ToLower(parsed.Path)
	mime := strings.ToLower(entry.MIMEType)
	requestType := strings.ToLower(header(entry.RequestHeaders, "content-type"))
	value, _ := parseJSON(entry.RequestBody)
	payload, object := value.(map[string]any)
	operation := ""
	graphql := strings.Contains(path, "graphql")
	if object {
		if _, ok := payload["query"]; ok {
			graphql = true
		}
		if name, ok := payload["operationName"].(string); ok {
			operation = safeName(name)
			graphql = true
		}
		if extensions, ok := payload["extensions"].(map[string]any); ok {
			if _, ok := extensions["persistedQuery"]; ok {
				return "graphql_persisted_query", operation
			}
		}
		if _, ok := payload["jsonrpc"]; ok {
			return "json_rpc", ""
		}
	}
	if name := parsed.Query().Get("operationName"); name != "" {
		operation = safeName(name)
		graphql = true
	}
	if graphql {
		return "graphql", operation
	}
	if strings.Contains(path, "batchexecute") {
		return "google_batchexecute", ""
	}
	if strings.Contains(path, "/trpc") {
		return "trpc", ""
	}
	if strings.Contains(mime, "grpc") || strings.Contains(requestType, "grpc") {
		return "grpc_web", ""
	}
	if parsed.Scheme == "ws" || parsed.Scheme == "wss" || strings.EqualFold(entry.ResourceType, "websocket") {
		return "websocket", ""
	}
	if strings.Contains(mime, "event-stream") {
		return "sse", ""
	}
	if _, ok := parseJSON(entry.ResponseBody); ok || strings.Contains(mime, "json") {
		return "rest_json", ""
	}
	if strings.Contains(mime, "html") {
		return "html", ""
	}
	return "http", ""
}

func authSignals(entry page.Exchange) []string {
	result := []string{}
	for name, value := range entry.RequestHeaders {
		lower := strings.ToLower(name)
		switch {
		case lower == "authorization":
			kind := "authorization"
			if strings.HasPrefix(strings.ToLower(value), "bearer ") {
				kind = "bearer"
			}
			result = appendUnique(result, kind)
		case lower == "cookie":
			result = appendUnique(result, "cookies")
		case strings.Contains(lower, "csrf") || strings.Contains(lower, "xsrf"):
			result = appendUnique(result, "csrf")
		case strings.Contains(lower, "api-key") || strings.Contains(lower, "apikey"):
			result = appendUnique(result, "api_key")
		}
	}
	parsed, err := url.Parse(entry.URL)
	if err == nil {
		for name := range parsed.Query() {
			if sensitiveName(name) {
				result = appendUnique(result, "query_credential")
			}
		}
	}
	return result
}

func sensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, word := range []string{"token", "secret", "password", "api_key", "apikey", "authorization", "session"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func paginationName(name string) bool {
	lower := strings.ToLower(name)
	for _, part := range []string{"cursor", "nextpage", "next_page", "nexttoken", "next_token", "hasnext", "has_next", "pagetoken", "page_token"} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	switch lower {
	case "page", "offset", "limit", "per_page", "after", "before", "next", "totalpages", "total_pages":
		return true
	}
	return false
}

func paginationSignals(endpoint *Endpoint, entry page.Exchange, query url.Values, evidence string) []Signal {
	result := []Signal{}
	add := func(location, name string) {
		result = append(result, Signal{EndpointID: endpoint.ID, Kind: "pagination", Location: location, Name: safeName(name), Basis: "inferred", Evidence: []string{evidence}})
	}
	for name := range query {
		if paginationName(name) {
			add("query", name)
		}
	}
	for _, group := range []struct {
		location string
		fields   []Field
	}{{"request_body", requestFields(entry.RequestBody, header(entry.RequestHeaders, "content-type"))}, {"response_body", schema(entry.ResponseBody)}} {
		for _, field := range group.fields {
			parts := strings.Split(field.Path, ".")
			name := strings.TrimSuffix(parts[len(parts)-1], "[]")
			if paginationName(name) {
				add(group.location, field.Path)
			}
		}
	}
	if strings.Contains(strings.ToLower(header(entry.ResponseHeaders, "link")), `rel="next"`) {
		add("response_header", "link")
	}
	return result
}

func protectionSignals(entry page.Exchange) []string {
	result := []string{}
	switch entry.Status {
	case 401:
		result = append(result, "authentication_required")
	case 403:
		result = append(result, "access_denied")
	case 429:
		result = append(result, "rate_limited")
	}
	lower := strings.ToLower(entry.ResponseBody)
	for _, marker := range []string{"cf-chl-", "challenge-platform", "challenges.cloudflare.com", "verify you are human"} {
		if strings.Contains(lower, marker) {
			result = append(result, "browser_challenge")
			break
		}
	}
	if header(entry.ResponseHeaders, "retry-after") != "" {
		result = append(result, "retry_after")
	}
	return result
}

func graphqlBatch(body string) []string {
	var payloads []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &payloads); err != nil {
		return nil
	}
	result := []string{}
	for _, payload := range payloads {
		var name string
		if err := json.Unmarshal(payload["operationName"], &name); err == nil && name != "" {
			result = appendUnique(result, safeName(name))
		}
	}
	return result
}
