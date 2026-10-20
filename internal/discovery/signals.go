package discovery

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/internal/page"
)

func detectProtocol(entry page.Exchange) (string, string) {
	mime := strings.ToLower(entry.MIMEType)
	requestType := strings.ToLower(header(entry.RequestHeaders, "content-type"))
	if isSOAP(entry, requestType, mime) {
		return "soap", ""
	}
	value, _ := parseJSON(entry.RequestBody)
	if batch, ok := value.([]any); ok {
		if protocol, operations := batchProtocol(batch); protocol != "" {
			return protocol, strings.Join(operations, ",")
		}
	}
	parsed, err := url.Parse(entry.URL)
	if err != nil {
		return "http", ""
	}
	path := strings.ToLower(parsed.Path)
	query := parsed.Query()
	payload, object := value.(map[string]any)
	operation := ""
	graphql := strings.Contains(path, "graphql") || strings.Contains(requestType, "application/graphql") || strings.Contains(mime, "application/graphql")
	if object {
		if document, ok := payload["query"].(string); ok && graphqlDocument(document) {
			graphql = true
		}
		if name, ok := payload["operationName"].(string); ok {
			operation = safeName(name)
			graphql = true
		}
		if persistedQuery(payload["extensions"]) {
			return "graphql_persisted_query", operation
		}
		if _, ok := payload["jsonrpc"]; ok {
			return "json_rpc", ""
		}
	}
	if name := query.Get("operationName"); name != "" {
		operation = safeName(name)
		graphql = true
	}
	if graphqlDocument(query.Get("query")) {
		graphql = true
	}
	if raw := query.Get("extensions"); raw != "" {
		var extensions any
		if json.Unmarshal([]byte(raw), &extensions) == nil && persistedQuery(extensions) {
			return "graphql_persisted_query", operation
		}
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
	if strings.Contains(mime, "grpc") || strings.Contains(requestType, "grpc") || hasHeader(entry.RequestHeaders, "x-grpc-web") {
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
			scheme, _, _ := strings.Cut(strings.TrimSpace(strings.ToLower(value)), " ")
			switch scheme {
			case "bearer":
				result = appendUnique(result, "bearer")
			case "basic", "digest", "negotiate", "ntlm":
				result = appendUnique(result, "authorization")
				result = appendUnique(result, scheme)
			default:
				result = appendUnique(result, "authorization")
			}
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

func batchProtocol(batch []any) (string, []string) {
	protocol := ""
	operations := []string{}
	for _, item := range batch {
		member, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := member["jsonrpc"]; ok {
			return "json_rpc", nil
		}
		document, _ := member["query"].(string)
		name, named := member["operationName"].(string)
		if named || graphqlDocument(document) || persistedQuery(member["extensions"]) {
			protocol = "graphql_batch"
		}
		if named && name != "" {
			operations = appendUnique(operations, safeName(name))
		}
	}
	return protocol, operations
}

func persistedQuery(extensions any) bool {
	object, ok := extensions.(map[string]any)
	if !ok {
		return false
	}
	_, ok = object["persistedQuery"]
	return ok
}

func graphqlDocument(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") {
		return true
	}
	for _, keyword := range []string{"query", "mutation", "subscription", "fragment"} {
		if rest, ok := strings.CutPrefix(value, keyword); ok && rest != "" && strings.ContainsRune(" \t\r\n{(@", rune(rest[0])) && strings.Contains(rest, "{") {
			return true
		}
	}
	return false
}
