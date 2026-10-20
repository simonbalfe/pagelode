package batch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Counts map[string]map[State]int64

type Totals struct {
	URLs   int64
	Done   int64
	Dead   int64
	Failed int64
}

func (t Totals) Finished() int64 {
	return t.Done + t.Dead + t.Failed
}

func (c Counts) Totals() Totals {
	http, browser := c[httpQueue], c[browserQueue]
	var urls, handedOff int64
	for _, n := range http {
		urls += n
	}
	for _, n := range browser {
		handedOff += n
	}
	return Totals{
		URLs:   urls,
		Done:   http[Done] - handedOff + browser[Done],
		Dead:   http[Dead] + browser[Dead],
		Failed: http[Failed] + browser[Failed],
	}
}

const (
	countsTimeout = 5 * time.Second
	rateWindow    = time.Minute
	stallAfter    = 30 * time.Second
	barWidth      = 24
)

type progressSample struct {
	at       time.Time
	finished int64
}

type progressMeter struct {
	samples []progressSample
}

func (m *progressMeter) observe(now time.Time, finished int64) (rate float64, window time.Duration) {
	m.samples = append(m.samples, progressSample{at: now, finished: finished})
	for len(m.samples) > 2 && now.Sub(m.samples[1].at) >= rateWindow {
		m.samples = m.samples[1:]
	}
	oldest := m.samples[0]
	window = now.Sub(oldest.at)
	if window < time.Second {
		return 0, 0
	}
	return float64(finished-oldest.finished) / window.Seconds(), window
}

func (r *runner) reportProgress(ctx context.Context) func() {
	if r.options.Progress == nil {
		return func() {}
	}
	meter := &progressMeter{}
	report := func(ctx context.Context) {
		r.writeProgress(ctx, meter)
	}
	report(ctx)
	stop := make(chan struct{})
	var reporter sync.WaitGroup
	reporter.Go(func() {
		ticker := time.NewTicker(r.options.ProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				report(ctx)
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
		}
	})
	return func() {
		close(stop)
		reporter.Wait()
		report(context.Background())
	}
}

func (r *runner) writeProgress(ctx context.Context, meter *progressMeter) {
	now := time.Now()
	p := palette{on: r.options.Color}
	stamp := p.dim(now.Format("15:04:05"))
	if r.options.Counts == nil {
		s := r.stats
		fmt.Fprintf(r.options.Progress, "%s  done %s  dead %s  failed %s  to browser %s\n", stamp, number(s.Done.Load()), number(s.Dead.Load()), number(s.Failed.Load()), number(s.NeedsBrowser.Load()))
		return
	}
	countsCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), countsTimeout)
	counts, err := r.options.Counts(countsCtx)
	cancel()
	if err != nil {
		fmt.Fprintf(r.options.Progress, "%s  %s\n", stamp, p.red("queue counts unavailable: "+err.Error()))
		return
	}
	totals := counts.Totals()
	finished := totals.Finished()
	remaining := totals.URLs - finished
	rate, window := meter.observe(now, finished)

	failed := p.dim("failed 0")
	if totals.Failed > 0 {
		failed = p.red("failed " + number(totals.Failed))
	}
	var speed string
	switch {
	case totals.URLs > 0 && remaining == 0:
		speed = p.green("complete")
	case window >= stallAfter && rate == 0:
		speed = p.boldRed("stalled " + window.Round(time.Second).String())
	case window == 0:
		speed = p.dim("measuring…")
	default:
		speed = p.bold(fmt.Sprintf("%.1f URLs/s", rate)) + p.dim("  ETA ") + p.bold(eta(remaining, rate))
	}
	separator := p.dim("│")
	overall := strings.Join([]string{
		stamp,
		bar(p, finished, totals.URLs) + " " + p.bold(fmt.Sprintf("%5s", percent(finished, totals.URLs))),
		p.bold(number(finished)) + p.dim(" / "+number(totals.URLs)),
		separator,
		p.green("done " + number(totals.Done)),
		p.yellow("dead " + number(totals.Dead)),
		failed,
		separator,
		speed,
	}, "  ")
	stages := strings.Repeat(" ", 10) + stageLine(p, "http", counts[httpQueue]) + "  " + separator + "  " + stageLine(p, "browser", counts[browserQueue])
	fmt.Fprintln(r.options.Progress, overall+"\n"+stages)
}

func stageLine(p palette, name string, counts map[State]int64) string {
	retrying := p.dim(fmt.Sprintf("retrying %-7s", number(counts[Retrying])))
	if counts[Retrying] > 0 {
		retrying = p.magenta(fmt.Sprintf("retrying %-7s", number(counts[Retrying])))
	}
	return p.bold(fmt.Sprintf("%-8s", name)) +
		p.cyan(fmt.Sprintf("queued %-7s", number(counts[Pending]))) + " " +
		p.blue(fmt.Sprintf("running %-5s", number(counts[Running]))) + " " +
		retrying
}

func bar(p palette, part, whole int64) string {
	filled := 0
	if whole > 0 {
		filled = int(part * barWidth / whole)
	}
	return p.green(strings.Repeat("█", filled)) + p.dim(strings.Repeat("░", barWidth-filled))
}

func eta(remaining int64, rate float64) string {
	if rate <= 0 {
		return "—"
	}
	return (time.Duration(float64(remaining)/rate) * time.Second).Round(time.Second).String()
}

func percent(part, whole int64) string {
	if whole == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)*100/float64(whole))
}

func number(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if n < 0 {
		return "-" + number(-n)
	}
	var out strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}

type palette struct {
	on bool
}

func (p palette) paint(code, text string) string {
	if !p.on {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (p palette) dim(text string) string     { return p.paint("2", text) }
func (p palette) bold(text string) string    { return p.paint("1", text) }
func (p palette) red(text string) string     { return p.paint("31", text) }
func (p palette) boldRed(text string) string { return p.paint("1;31", text) }
func (p palette) green(text string) string   { return p.paint("32", text) }
func (p palette) yellow(text string) string  { return p.paint("33", text) }
func (p palette) blue(text string) string    { return p.paint("34", text) }
func (p palette) magenta(text string) string { return p.paint("35", text) }
func (p palette) cyan(text string) string    { return p.paint("36", text) }
