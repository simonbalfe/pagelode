package fetch

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed signatures.json
var raw []byte

type Pattern struct {
	Label string
	Value *regexp.Regexp
}

type Header struct {
	Label string
	Name  string
	Value string
}

type Signatures struct {
	Blocks           []Pattern
	ShortBlocks      []Pattern
	ChallengeHeaders []Header
	ChallengeURLs    []string
}

type file struct {
	Blocks      []patternFile `json:"blocks"`
	ShortBlocks []patternFile `json:"short_blocks"`
	Challenge   struct {
		Headers     []headerFile `json:"headers"`
		URLContains []string     `json:"url_contains"`
	} `json:"challenge"`
}

type patternFile struct {
	Label   string `json:"label"`
	Pattern string `json:"pattern"`
	Flags   string `json:"flags"`
}

type headerFile struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func MustLoadSignatures() Signatures {
	set, err := parse(raw)
	if err != nil {
		panic(err)
	}
	return set
}

func parse(data []byte) (Signatures, error) {
	var decoded file
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Signatures{}, fmt.Errorf("signatures: decode: %w", err)
	}
	blocks, err := compile(decoded.Blocks)
	if err != nil {
		return Signatures{}, err
	}
	shortBlocks, err := compile(decoded.ShortBlocks)
	if err != nil {
		return Signatures{}, err
	}
	headers := make([]Header, 0, len(decoded.Challenge.Headers))
	for _, header := range decoded.Challenge.Headers {
		headers = append(headers, Header{Label: header.Label, Name: strings.ToLower(header.Name), Value: strings.ToLower(header.Value)})
	}
	return Signatures{Blocks: blocks, ShortBlocks: shortBlocks, ChallengeHeaders: headers, ChallengeURLs: decoded.Challenge.URLContains}, nil
}

func compile(patterns []patternFile) ([]Pattern, error) {
	compiled := make([]Pattern, 0, len(patterns))
	for _, pattern := range patterns {
		if strings.Trim(pattern.Flags, "is") != "" {
			return nil, fmt.Errorf("signatures: %s: unsupported flags %q", pattern.Label, pattern.Flags)
		}
		expression := pattern.Pattern
		if pattern.Flags != "" {
			expression = "(?" + pattern.Flags + ")" + expression
		}
		value, err := regexp.Compile(expression)
		if err != nil {
			return nil, fmt.Errorf("signatures: %s: %w", pattern.Label, err)
		}
		compiled = append(compiled, Pattern{Label: pattern.Label, Value: value})
	}
	return compiled, nil
}
