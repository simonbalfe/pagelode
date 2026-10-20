package urlshape

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestShape(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"root", "https://Example.com/", "example.com||0|"},
		{"no path", "https://example.com", "example.com||0|"},
		{"simple", "https://example.com/blog/hello", "example.com|blog|2|"},
		{"numeric segment", "https://example.com/post/123", "example.com|post|2|"},
		{"numeric first segment", "https://example.com/123/edit", "example.com|#|2|"},
		{"uuid", "https://example.com/u/123e4567-e89b-12d3-a456-426614174000/x", "example.com|u|3|"},
		{"hex", "https://example.com/c/deadbeef01", "example.com|c|2|"},
		{"short hex is a word", "https://example.com/c/face", "example.com|c|2|"},
		{"trailing slash ignored", "https://example.com/blog/hello/", "example.com|blog|2|"},
		{"query names sorted", "https://example.com/s?b=1&a=2&b=3", "example.com|s|1|a,b"},
		{"query values ignored", "https://example.com/s?q=x", "example.com|s|1|q"},
		{"host case", "HTTPS://EXAMPLE.COM/A", "example.com|A|1|"},
		{"malformed returned unchanged", "http://[::1", "http://[::1"},
		{"empty", "", "||0|"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Shape(tt.url); got != tt.want {
				t.Fatalf("Shape(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestShapeGroupsInstances(t *testing.T) {
	a := Shape("https://example.com/post/1?utm=a")
	b := Shape("https://example.com/post/22?utm=b")
	c := Shape("https://example.com/post/22/comments?utm=b")
	if a != b {
		t.Fatalf("expected equal shapes, got %q and %q", a, b)
	}
	if a == c {
		t.Fatalf("expected different shapes, both %q", a)
	}
}

func TestSampler(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		urls  []string
		want  []bool
	}{
		{
			name:  "limit per shape",
			limit: 2,
			urls: []string{
				"https://e.com/p/1", "https://e.com/p/2", "https://e.com/p/3",
				"https://e.com/about", "https://e.com/p/4",
			},
			want: []bool{true, true, false, true, false},
		},
		{
			name:  "limit one",
			limit: 1,
			urls:  []string{"https://e.com/a/x", "https://e.com/a/y", "https://f.com/a/x"},
			want:  []bool{true, false, true},
		},
		{
			name:  "zero limit is unlimited",
			limit: 0,
			urls:  []string{"https://e.com/a", "https://e.com/a", "https://e.com/a"},
			want:  []bool{true, true, true},
		},
		{
			name:  "query names split shapes",
			limit: 1,
			urls:  []string{"https://e.com/s?a=1", "https://e.com/s?b=1", "https://e.com/s?a=2"},
			want:  []bool{true, true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSampler(tt.limit)
			for i, u := range tt.urls {
				if got := s.Allow(u); got != tt.want[i] {
					t.Fatalf("Allow(%q) #%d = %v, want %v", u, i, got, tt.want[i])
				}
			}
		})
	}
}

func TestSamplerConcurrent(t *testing.T) {
	s := NewSampler(5)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if s.Allow(fmt.Sprintf("https://e.com/item/%d", i)) {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if allowed.Load() != 5 {
		t.Fatalf("allowed %d, want 5", allowed.Load())
	}
}

func TestTemplates(t *testing.T) {
	tests := []struct {
		name      string
		urls      []string
		threshold int
		want      []Template
	}{
		{name: "empty", urls: nil, threshold: 3, want: []Template{}},
		{
			name: "tags and authors",
			urls: []string{
				"https://e.com/tag/a", "https://e.com/tag/b", "https://e.com/tag/c",
				"https://e.com/author/1", "https://e.com/author/2",
			},
			threshold: 3,
			want: []Template{
				{Pattern: "/tag/*", Count: 3, Samples: []string{"https://e.com/tag/a", "https://e.com/tag/b", "https://e.com/tag/c"}},
				{Pattern: "/author/*", Count: 2, Samples: []string{"https://e.com/author/1", "https://e.com/author/2"}},
			},
		},
		{
			name:      "below threshold stays literal",
			urls:      []string{"https://e.com/tag/x", "https://e.com/tag/y", "https://e.com/about/team"},
			threshold: 3,
			want: []Template{
				{Pattern: "/about/team", Count: 1, Samples: []string{"https://e.com/about/team"}},
				{Pattern: "/tag/x", Count: 1, Samples: []string{"https://e.com/tag/x"}},
				{Pattern: "/tag/y", Count: 1, Samples: []string{"https://e.com/tag/y"}},
			},
		},
		{
			name:      "threshold two collapses tag children",
			urls:      []string{"https://e.com/tag/x", "https://e.com/tag/y"},
			threshold: 2,
			want: []Template{
				{Pattern: "/tag/*", Count: 2, Samples: []string{"https://e.com/tag/x", "https://e.com/tag/y"}},
			},
		},
		{
			name:      "ids collapse without threshold",
			urls:      []string{"https://e.com/o/1", "https://e.com/o/2/", "https://e.com/o/123e4567-e89b-12d3-a456-426614174000"},
			threshold: 0,
			want: []Template{
				{Pattern: "/o/*", Count: 3, Samples: []string{"https://e.com/o/1", "https://e.com/o/2/", "https://e.com/o/123e4567-e89b-12d3-a456-426614174000"}},
			},
		},
		{
			name: "deep collapse merges subtrees",
			urls: []string{
				"https://e.com/blog/a/comments", "https://e.com/blog/b/comments", "https://e.com/blog/c/comments",
				"https://e.com/blog/c/edit",
			},
			threshold: 3,
			want: []Template{
				{Pattern: "/blog/*/comments", Count: 3, Samples: []string{"https://e.com/blog/a/comments", "https://e.com/blog/b/comments", "https://e.com/blog/c/comments"}},
				{Pattern: "/blog/*/edit", Count: 1, Samples: []string{"https://e.com/blog/c/edit"}},
			},
		},
		{
			name:      "query names appended and sorted",
			urls:      []string{"https://e.com/s?b=1&a=2", "https://e.com/s?a=9&b=8", "https://e.com/s"},
			threshold: 5,
			want: []Template{
				{Pattern: "/s?a&b", Count: 2, Samples: []string{"https://e.com/s?b=1&a=2", "https://e.com/s?a=9&b=8"}},
				{Pattern: "/s", Count: 1, Samples: []string{"https://e.com/s"}},
			},
		},
		{
			name:      "samples capped at three and duplicates not repeated",
			urls:      []string{"https://e.com/p/1", "https://e.com/p/1", "https://e.com/p/2", "https://e.com/p/3", "https://e.com/p/4"},
			threshold: 0,
			want: []Template{
				{Pattern: "/p/*", Count: 5, Samples: []string{"https://e.com/p/1", "https://e.com/p/2", "https://e.com/p/3"}},
			},
		},
		{
			name:      "top level sections stay literal at threshold two",
			urls:      []string{"https://e.com/a/1x", "https://e.com/b/1x", "https://e.com/c/1x"},
			threshold: 2,
			want: []Template{
				{Pattern: "/a/1x", Count: 1, Samples: []string{"https://e.com/a/1x"}},
				{Pattern: "/b/1x", Count: 1, Samples: []string{"https://e.com/b/1x"}},
				{Pattern: "/c/1x", Count: 1, Samples: []string{"https://e.com/c/1x"}},
			},
		},
		{
			name:      "root path",
			urls:      []string{"https://e.com/", "https://e.com"},
			threshold: 3,
			want:      []Template{{Pattern: "/", Count: 2, Samples: []string{"https://e.com/", "https://e.com"}}},
		},
		{
			name:      "malformed and empty skipped",
			urls:      []string{"http://[::1", "", "https://e.com/a"},
			threshold: 3,
			want:      []Template{{Pattern: "/a", Count: 1, Samples: []string{"https://e.com/a"}}},
		},
		{
			name: "hosts tracked separately but patterns merge",
			urls: []string{
				"https://a.com/t/x", "https://a.com/t/y", "https://b.com/t/x",
			},
			threshold: 2,
			want: []Template{
				{Pattern: "/t/*", Count: 2, Samples: []string{"https://a.com/t/x", "https://a.com/t/y"}},
				{Pattern: "/t/x", Count: 1, Samples: []string{"https://b.com/t/x"}},
			},
		},
		{
			name:      "count tie sorted by pattern",
			urls:      []string{"https://e.com/b", "https://e.com/a"},
			threshold: 5,
			want: []Template{
				{Pattern: "/a", Count: 1, Samples: []string{"https://e.com/a"}},
				{Pattern: "/b", Count: 1, Samples: []string{"https://e.com/b"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Templates(tt.urls, tt.threshold)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Templates() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTemplatesFirstSegmentNotCollapsedByThreshold(t *testing.T) {
	var urls []string
	var want []Template
	for i := range 12 {
		u := fmt.Sprintf("https://e.com/a%02d/x", i)
		urls = append(urls, u)
		want = append(want, Template{Pattern: fmt.Sprintf("/a%02d/x", i), Count: 1, Samples: []string{u}})
	}
	got := Templates(urls, 10)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Templates() = %+v, want %+v", got, want)
	}
}

func TestTemplatesDeeperPositionsCollapse(t *testing.T) {
	tests := []struct {
		name  string
		urls  func() []string
		want  string
		wantN int
	}{
		{"tags", func() []string { return numbered("https://e.com/tag/t%02d", 30) }, "/tag/*", 30},
		{"post slugs", func() []string { return numbered("https://e.com/post/slug-%d-word", 30) }, "/post/*", 30},
		{"first segment id", func() []string { return []string{"https://e.com/1/a", "https://e.com/2/a"} }, "/*/a", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Templates(tt.urls(), 10)
			if len(got) != 1 || got[0].Pattern != tt.want || got[0].Count != tt.wantN || len(got[0].Samples) != 3 && tt.wantN >= 3 {
				t.Fatalf("Templates() = %+v, want single %q count %d", got, tt.want, tt.wantN)
			}
		})
	}
}

func numbered(format string, n int) []string {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = fmt.Sprintf(format, i)
	}
	return urls
}
