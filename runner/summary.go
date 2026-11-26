package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/vflame6/leaker/runner/sources"
)

// Statuses of a source in the run summary.
const (
	StatusOK          = "ok"
	StatusCached      = "cached" // the local database answered
	StatusTruncated   = "truncated"
	StatusNoKey       = "no key"
	StatusUnsupported = "unsupported"
	StatusAuth        = "auth"
	StatusQuota       = "quota"
	StatusRateLimited = "rate-limited"
	StatusError       = "error"
)

// defaultSummaryOutput receives the per-target tables when
// Options.SummaryOutput is not set.
var defaultSummaryOutput io.Writer = os.Stderr

// SourceSummary is one source's row for one target.
type SourceSummary struct {
	Source  string `json:"source"`
	Status  string `json:"status"`
	Fetched int    `json:"fetched"`
	// Total is the number of records the API says exist; omitted when the
	// API does not say.
	Total       *int   `json:"total,omitempty"`
	Kept        int    `json:"kept"`
	Requests    int    `json:"requests"`
	CreditsLeft *int   `json:"credits_left,omitempty"`
	CreditsUsed *int   `json:"credits_used,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Error       string `json:"error,omitempty"`
	TimeMS      int64  `json:"time_ms"`
}

// skipped reports whether the source did not run for the target.
func (s SourceSummary) skipped() bool {
	return s.Status == StatusNoKey || s.Status == StatusUnsupported
}

// TargetSummary is the table of one target.
type TargetSummary struct {
	Target  string          `json:"target"`
	Found   int             `json:"found"`
	TimeMS  int64           `json:"time_ms"`
	Sources []SourceSummary `json:"sources"`
}

// RunSummary is what --stats-file saves: every target's table.
type RunSummary struct {
	Version string          `json:"version"`
	Type    string          `json:"type"`
	Started time.Time       `json:"started"`
	TimeMS  int64           `json:"time_ms"`
	Targets []TargetSummary `json:"targets"`
}

// sourceRun is what one source returned for one target.
type sourceRun struct {
	source  sources.Source
	results []sources.Result
	stats   sources.Stats
	err     error
	elapsed time.Duration
}

// failed reports whether the source returned an error and nothing else.
func (r *sourceRun) failed() bool {
	return r.err != nil && len(r.results) == 0
}

// status is the summary status of a run.
func (r *sourceRun) status() string {
	switch {
	case errors.Is(r.err, sources.ErrAuth):
		return StatusAuth
	case errors.Is(r.err, sources.ErrQuota):
		return StatusQuota
	case errors.Is(r.err, sources.ErrRateLimited):
		return StatusRateLimited
	case r.err != nil:
		return StatusError
	case r.source.Name() == sources.LocalSourceName:
		return StatusCached
	case r.stats.Truncated:
		return StatusTruncated
	}
	return StatusOK
}

// summary is the row of a run whose results kept survived filtering and
// deduplication.
func (r *sourceRun) summary(kept int) SourceSummary {
	s := SourceSummary{
		Source:      r.source.Name(),
		Status:      r.status(),
		Fetched:     r.stats.Fetched,
		Kept:        kept,
		Requests:    r.stats.Requests,
		CreditsLeft: r.stats.CreditsLeft,
		CreditsUsed: r.stats.CreditsUsed,
		Truncated:   r.stats.Truncated,
		TimeMS:      r.elapsed.Milliseconds(),
	}
	if r.stats.Total >= 0 {
		total := r.stats.Total
		s.Total = &total
	}
	if r.err != nil {
		s.Error = r.err.Error()
	}
	return s
}

// writeSummaryTable prints the table of one target in one write, so log
// lines of other goroutines do not land inside it.
func writeSummaryTable(w io.Writer, t TargetSummary) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SOURCE\tSTATUS\tFETCHED\tKEPT\tREQUESTS\tCREDITS\tTIME")
	for _, s := range t.Sources {
		if s.skipped() {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\t-\t-\n", s.Source, s.Status)
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			s.Source, s.Status, fetchedCell(s), s.Kept, s.Requests, creditsCell(s), timeCell(s.TimeMS))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// fetchedCell is "fetched of total", or "fetched" when the total is unknown.
func fetchedCell(s SourceSummary) string {
	if s.Total == nil {
		return strconv.Itoa(s.Fetched)
	}
	return fmt.Sprintf("%d of %d", s.Fetched, *s.Total)
}

// creditsCell shows what the search cost and what is left, as reported.
func creditsCell(s SourceSummary) string {
	switch {
	case s.CreditsUsed != nil && s.CreditsLeft != nil:
		return fmt.Sprintf("%d used, %d left", *s.CreditsUsed, *s.CreditsLeft)
	case s.CreditsUsed != nil:
		return fmt.Sprintf("%d used", *s.CreditsUsed)
	case s.CreditsLeft != nil:
		return fmt.Sprintf("%d left", *s.CreditsLeft)
	}
	return "-"
}

// timeCell rounds a duration for the table: 340ms, 1.2s.
func timeCell(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return d.String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// truncationNotice tells what a truncated search left behind and how to get
// more: "dehashed: 1000 of 4213 fetched; raise --max-results or use --all".
// sourceDefault is true when the source searched with its own default
// request budget (Limits.DefaultRequests), which the hint then names.
func truncationNotice(source string, stats sources.Stats, limits sources.Limits, sourceDefault bool) string {
	fetched := strconv.Itoa(stats.Fetched)
	if stats.Total >= 0 {
		fetched = fmt.Sprintf("%d of %d", stats.Fetched, stats.Total)
	}
	notice := fmt.Sprintf("%s: %s fetched", source, fetched)
	if stats.Note != "" {
		notice += " " + stats.Note
	}
	switch stats.Limit {
	case sources.LimitResults:
		return notice + "; raise --max-results or use --all"
	case sources.LimitRequests:
		if sourceDefault {
			return fmt.Sprintf("%s; raise --max-requests (default %d for %s) or use --all", notice, limits.DefaultRequests, source)
		}
		return notice + "; raise --max-requests or use --all"
	case sources.LimitAPI:
		if limits.MaxResults > 0 {
			return fmt.Sprintf("%s; the API returns at most %d per search", notice, limits.MaxResults)
		}
		return notice + "; the API serves no more"
	case sources.LimitSafety:
		return fmt.Sprintf("%s; stopped after %d requests, give --max-requests to fetch more", notice, sources.MaxPagesWithoutLimit)
	}
	return notice
}

// writeRunSummary saves the summary as indented JSON.
func writeRunSummary(w io.Writer, run RunSummary) error {
	if run.Targets == nil {
		run.Targets = []TargetSummary{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(run)
}
