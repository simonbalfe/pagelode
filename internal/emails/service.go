package emails

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/profile"
)

type PageLoader interface {
	Load(context.Context, string, Options) (page.Document, error)
}

type Service struct {
	loader            PageLoader
	limiter           *limit.Limiter
	concurrency       int
	profilesDirectory string
}

func New(loader PageLoader, limiter *limit.Limiter, concurrency int, profilesDirectory string) *Service {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 4 {
		concurrency = 4
	}
	return &Service{loader: loader, limiter: limiter, concurrency: concurrency, profilesDirectory: profilesDirectory}
}

func (s *Service) Find(ctx context.Context, request Request) (Report, error) {
	request, err := request.Validate()
	if err != nil {
		return Report{}, err
	}
	options := Options{Render: request.Render}
	if request.Profile != "" {
		options.ProfileDirectory, err = profile.Directory(s.profilesDirectory, request.Profile, false)
		if err != nil {
			return Report{}, err
		}
	}
	started := time.Now()
	deadline, cancel := context.WithTimeout(ctx, time.Duration(request.MaxDurationMS)*time.Millisecond)
	defer cancel()
	scope, err := url.Parse(request.URL)
	if err != nil {
		return Report{}, err
	}
	run := search{request: request, scope: scope, report: Report{URL: publicURL(request.URL), Outcome: "ok", Emails: []Address{}, Pages: []Page{}}, seen: map[string]bool{}, visited: map[string]bool{}, addresses: map[string]int{}}
	run.add(target{url: request.URL, score: 200})
	if request.Profile == "" && request.MaxPages > 1 {
		sitemap := *run.scope
		sitemap.Path = "/sitemap.xml"
		sitemap.RawQuery = ""
		run.add(target{url: sitemap.String(), sitemap: true, score: 110})
	}
	concurrency := s.concurrency
	if request.Profile != "" {
		concurrency = 1
	}
	for len(run.queue) > 0 && len(run.report.Pages) < request.MaxPages && len(run.report.Emails) < request.MaxEmails && deadline.Err() == nil {
		orderTargets(run.queue)
		batch := run.take(concurrency)
		if len(batch) == 0 {
			break
		}
		for _, result := range s.loadBatch(deadline, batch, options) {
			if errors.Is(result.err, limit.ErrSaturated) {
				return Report{}, result.err
			}
			run.consume(result)
		}
	}
	if ctx.Err() != nil {
		return Report{}, ctx.Err()
	}
	if deadline.Err() != nil {
		run.warn("duration limit reached")
	}
	return run.finish(started), nil
}

type loaded struct {
	target   target
	document page.Document
	err      error
}

func (s *Service) loadBatch(ctx context.Context, batch []target, options Options) []loaded {
	results := make([]loaded, len(batch))
	var workers sync.WaitGroup
	for i, item := range batch {
		workers.Go(func() {
			current := options
			if item.sitemap {
				current = Options{Render: "never"}
			}
			pageCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			results[i] = loaded{target: item}
			results[i].err = s.limiter.Run(pageCtx, func() error {
				var err error
				results[i].document, err = s.loader.Load(pageCtx, item.url, current)
				return err
			})
		})
	}
	workers.Wait()
	return results
}
