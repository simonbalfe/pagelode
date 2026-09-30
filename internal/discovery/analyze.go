package discovery

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/page"
)

func Analyze(target string, document page.Document) Report {
	result := Report{Page: PageInfo{URL: sanitizeURL(target), FinalURL: sanitizeURL(document.FinalURL), Title: document.Title, Provider: document.Provider, Status: document.StatusCode}, Outcome: "ok", Endpoints: []Endpoint{}, Evidence: []Evidence{}, Protocols: []Signal{}, Auth: []Signal{}, Protections: []Signal{}, Sequences: []Sequence{}, Pagination: []Signal{}, DataSources: []DataSource{}, Warnings: []string{}, Attempts: []orchestrator.Attempt{}}
	if document.Traffic == nil {
		result.Warnings = append(result.Warnings, "network capture unavailable")
		return result
	}
	capture := document.Traffic
	result.Summary = Summary{Requests: len(capture.Entries), Dropped: capture.Dropped, DurationMS: capture.DurationMS}
	groups := map[string]int{}
	for index, entry := range capture.Entries {
		analyzeEntry(&result, groups, index, entry)
	}
	sort.SliceStable(result.Sequences, func(i, j int) bool { return result.Sequences[i].StartedMS < result.Sequences[j].StartedMS })
	result.Summary.EndpointCount = len(result.Endpoints)
	embeddedData(&result, document.HTML)
	if capture.Dropped > 0 {
		result.Warnings = append(result.Warnings, "request limit reached; some requests were not retained")
	}
	if result.Summary.Truncated > 0 {
		result.Warnings = append(result.Warnings, "body limits reached; some schemas may be incomplete")
	}
	if result.Summary.Incomplete > 0 {
		result.Warnings = append(result.Warnings, "some requests or bodies were incomplete during the observation window")
	}
	if result.Summary.DataRequests == 0 {
		result.Warnings = append(result.Warnings, "no data endpoint observed during this page load")
	}
	result.Warnings = append(result.Warnings, "endpoint descriptions are inferred from observed traffic; direct replay is untested")
	return result
}

func analyzeEntry(result *Report, groups map[string]int, index int, entry page.Exchange) {
	evidenceID := fmt.Sprintf("r%d", index+1)
	classification, reason := classifyEntry(entry)
	result.Evidence = append(result.Evidence, Evidence{ID: evidenceID, URL: sanitizeURL(entry.URL), Method: entry.Method, Status: entry.Status, ResourceType: entry.ResourceType, Classification: classification, Score: scoreEntry(entry, defaultBlocklist()), Reason: reason, FrameID: entry.FrameID, Initiator: entry.Initiator, StartedMS: entry.StartedMS, Truncated: entry.Truncated, Incomplete: entry.Error != ""})
	if entry.Truncated {
		result.Summary.Truncated++
	}
	if entry.Error != "" {
		result.Summary.Incomplete++
	}
	if classification == "noise" {
		result.Summary.NoiseRequests++
		return
	}
	parsed, err := url.Parse(entry.URL)
	if err != nil {
		return
	}
	protocol, operation := detectProtocol(entry)
	path := normalizeEntryPath(entry.URL)
	method := strings.ToUpper(strings.TrimSpace(entry.Method))
	host := strings.ToLower(extractHost(entry.URL))
	key := host + " " + method + " " + path
	position, found := groups[key]
	if !found {
		position = len(result.Endpoints)
		groups[key] = position
		result.Endpoints = append(result.Endpoints, Endpoint{ID: fmt.Sprintf("e%d", position+1), Host: host, Method: method, Path: path, Protocol: protocol, Candidate: candidateOperation(method, path), Statuses: []int{}, Parameters: []Field{}, RequestSchema: []Field{}, ResponseSchema: []Field{}, Auth: []string{}, Evidence: []string{}, Replayability: "untested"})
	}
	endpoint := &result.Endpoints[position]
	if operation != "" {
		endpoint.Operations = appendUnique(endpoint.Operations, operation)
		sort.Strings(endpoint.Operations)
		endpoint.Operation = ""
		if len(endpoint.Operations) == 1 {
			endpoint.Operation = endpoint.Operations[0]
		}
	}
	endpoint.Calls++
	endpoint.Evidence = append(endpoint.Evidence, evidenceID)
	if !hasStatus(endpoint.Statuses, entry.Status) {
		endpoint.Statuses = append(endpoint.Statuses, entry.Status)
		sort.Ints(endpoint.Statuses)
	}
	parameters := queryFields(parsed.Query(), "query.")
	for _, segment := range strings.Split(path, "/") {
		if segment == "{id}" || segment == "{uuid}" || segment == "{hash}" {
			parameters = append(parameters, Field{Path: "path." + strings.Trim(segment, "{}"), Types: []string{"string"}})
		}
	}
	endpoint.Parameters = mergeFields(endpoint.Parameters, parameters, evidenceID)
	endpoint.RequestSchema = mergeFields(endpoint.RequestSchema, requestFields(entry.RequestBody, header(entry.RequestHeaders, "content-type")), evidenceID)
	endpoint.ResponseSchema = mergeFields(endpoint.ResponseSchema, schema(entry.ResponseBody), evidenceID)
	for _, auth := range authSignals(entry) {
		endpoint.Auth = appendUnique(endpoint.Auth, auth)
		addSignal(&result.Auth, Signal{EndpointID: endpoint.ID, Kind: auth, Basis: "observed", Evidence: []string{evidenceID}})
	}
	addSignal(&result.Protocols, Signal{EndpointID: endpoint.ID, Kind: protocol, Basis: "inferred", Evidence: []string{evidenceID}})
	for _, signal := range paginationSignals(endpoint, entry, parsed.Query(), evidenceID) {
		addSignal(&result.Pagination, signal)
	}
	for _, kind := range protectionSignals(entry) {
		addSignal(&result.Protections, Signal{EndpointID: endpoint.ID, Kind: kind, Basis: "observed", Evidence: []string{evidenceID}})
	}
	result.Sequences = append(result.Sequences, Sequence{EvidenceID: evidenceID, EndpointID: endpoint.ID, StartedMS: entry.StartedMS})
	if classification == "data" {
		result.Summary.DataRequests++
		addDataSources(result, *endpoint, evidenceID)
	}
}

func classifyEntry(entry page.Exchange) (string, string) {
	score := scoreEntry(entry, defaultBlocklist())
	if score > 0 {
		return "data", fmt.Sprintf("printing press API score %d > 0", score)
	}
	return "noise", fmt.Sprintf("printing press API score %d <= 0", score)
}

func sanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.Fragment = ""
	values := parsed.Query()
	sanitized := url.Values{}
	for name := range values {
		sanitized.Set(safeName(name), "[redacted]")
	}
	parsed.RawQuery = sanitized.Encode()
	parts := strings.Split(parsed.Path, "/")
	for i, part := range parts {
		if len(part) > 100 {
			parts[i] = "[redacted]"
		}
	}
	parsed.Path = strings.Join(parts, "/")
	parsed.RawPath = ""
	return parsed.String()
}

func candidateOperation(method, path string) string {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "search") {
		return "search"
	}
	if method == "GET" && (strings.Contains(path, "{id}") || strings.Contains(path, "{uuid}") || strings.Contains(path, "{hash}")) {
		return "detail"
	}
	if method == "GET" {
		return "list_or_document"
	}
	return "request"
}
func hasStatus(values []int, value int) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func header(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func addSignal(signals *[]Signal, incoming Signal) {
	for i := range *signals {
		signal := &(*signals)[i]
		if signal.EndpointID == incoming.EndpointID && signal.Kind == incoming.Kind && signal.Location == incoming.Location && signal.Name == incoming.Name {
			for _, id := range incoming.Evidence {
				signal.Evidence = appendUnique(signal.Evidence, id)
			}
			return
		}
	}
	*signals = append(*signals, incoming)
}

func addDataSources(result *Report, endpoint Endpoint, evidence string) {
	locations := []string{}
	for _, field := range endpoint.ResponseSchema {
		for _, kind := range field.Types {
			if kind == "array" {
				locations = append(locations, field.Path)
			}
		}
	}
	if len(locations) == 0 {
		locations = []string{"$"}
	}
	for _, location := range locations {
		found := false
		for i := range result.DataSources {
			source := &result.DataSources[i]
			if source.EndpointID == endpoint.ID && source.Location == location {
				source.Evidence = appendUnique(source.Evidence, evidence)
				found = true
				break
			}
		}
		if !found {
			result.DataSources = append(result.DataSources, DataSource{EndpointID: endpoint.ID, Kind: endpoint.Protocol, Location: location, Basis: "candidate", Evidence: []string{evidence}})
		}
	}
}

func embeddedData(result *Report, html string) {
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return
	}
	dom.Find(`script[type="application/ld+json"], script[type="application/json"], script#__NEXT_DATA__`).Each(func(index int, selection *goquery.Selection) {
		if index >= 20 {
			return
		}
		if _, ok := parseJSON(selection.Text()); !ok {
			return
		}
		result.DataSources = append(result.DataSources, DataSource{Kind: "embedded_json", Location: fmt.Sprintf("script[%d]", index), Basis: "observed", Evidence: []string{"page"}})
	})
}
