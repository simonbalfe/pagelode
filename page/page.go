// Package page defines the provider-neutral document, session, and capture
// types exchanged between PageLode's fetchers, classifiers, and extractors.
package page

// Provider identifies which fetch path produced a Document.
type Provider string

// Provider values name the plain TLS client, the Patchright browser,
// the CapSolver service, and replay of a captured browser session.
const (
	ProviderTLS        Provider = "tls"
	ProviderPatchright Provider = "patchright"
	ProviderCapSolver  Provider = "capsolver"
	ProviderReplay     Provider = "replay"
)

// ContentType classifies a response body by how PageLode can process it.
type ContentType string

// ContentType values; ContentUnknown marks bodies PageLode does not extract.
const (
	ContentHTML    ContentType = "html"
	ContentText    ContentType = "text"
	ContentPDF     ContentType = "pdf"
	ContentUnknown ContentType = "unknown"
)

// Cookie is a browser-portable cookie that can move between fetch providers.
// Expires is Unix time in seconds; zero means a session cookie.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"http_only,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"same_site,omitempty"`
}

// Session carries the identity a fetch ran with so later requests can reuse it.
// ProxyURL and Headers are never serialised to JSON.
type Session struct {
	UserAgent string            `json:"user_agent,omitempty"`
	Cookies   []Cookie          `json:"cookies,omitempty"`
	ProxyURL  string            `json:"-"`
	Headers   map[string]string `json:"-"`
}

// Replayable reports whether the session has the proxy, headers, and cookies
// needed to replay a browser session through an HTTP client.
func (s Session) Replayable() bool {
	return s.ProxyURL != "" && len(s.Headers) > 0 && len(s.Cookies) > 0
}

// Document is the result of fetching one URL with any provider.
// HTML holds markup for HTML responses and Text holds plain-text bodies;
// Challenge, Traffic, and ExploredURLs are set only by providers that produce them.
type Document struct {
	Provider     Provider
	StatusCode   int
	FinalURL     string
	Title        string
	HTML         string
	Text         string
	Type         ContentType
	Headers      map[string]string
	Challenge    *Challenge
	Session      Session
	Traffic      *Capture
	ExploredURLs []string
}

// ChallengeVendor identifies the bot-protection vendor behind a Challenge.
type ChallengeVendor string

// ChallengeVendor values for the challenges PageLode can detect.
const (
	ChallengeCloudflare ChallengeVendor = "cloudflare"
	ChallengeDataDome   ChallengeVendor = "datadome"
	ChallengeRecaptcha  ChallengeVendor = "recaptcha"
)

// Challenge describes a bot-protection challenge detected on a page, with the
// parameters a solver needs; SiteKey and DataS apply to reCAPTCHA.
type Challenge struct {
	Vendor  ChallengeVendor `json:"vendor"`
	URL     string          `json:"url,omitempty"`
	SiteKey string          `json:"sitekey,omitempty"`
	DataS   string          `json:"data_s,omitempty"`
}
