package page

type Provider string

const (
	ProviderTLS        Provider = "tls"
	ProviderRod        Provider = "rod"
	ProviderPatchright Provider = "patchright"
)

type ContentType string

const (
	ContentHTML    ContentType = "html"
	ContentText    ContentType = "text"
	ContentPDF     ContentType = "pdf"
	ContentUnknown ContentType = "unknown"
)

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

type Session struct {
	UserAgent string   `json:"user_agent,omitempty"`
	Cookies   []Cookie `json:"cookies,omitempty"`
}

type Document struct {
	Provider   Provider
	StatusCode int
	FinalURL   string
	Title      string
	HTML       string
	Text       string
	Type       ContentType
	Session    Session
}
