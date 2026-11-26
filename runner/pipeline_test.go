package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// timedSource takes delay(target) per search, records when it started each
// target and emits one result naming the target.
type timedSource struct {
	fakeSource
	delay func(target string) time.Duration

	mu     sync.Mutex
	starts []time.Time
	order  []string
}

func (s *timedSource) Search(ctx context.Context, q sources.Query, _ *sources.Session, emit func(sources.Result)) (sources.Stats, error) {
	s.mu.Lock()
	s.starts = append(s.starts, time.Now())
	s.order = append(s.order, q.Target)
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return sources.Stats{Total: -1}, ctx.Err()
	case <-time.After(s.delay(q.Target)):
	}
	emit(sources.Result{Source: s.name, Email: q.Target, Password: s.name})
	return sources.Stats{Total: -1}, nil
}

func (s *timedSource) started() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.starts...)
}

func emailTargets(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "user%d@example.com\n", i)
	}
	return b.String()
}

// TestPipeline_FastSourceDoesNotWaitForSlow is the B4 acceptance check: in
// the four-target mock, ProxyNova has answered all four targets within 3s
// while the slow source is still on its second.
func TestPipeline_FastSourceDoesNotWaitForSlow(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 8 seconds")
	}
	captureLogs(t)
	captureSummary(t)
	var mu sync.Mutex
	var answered []time.Time
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"count":0,"lines":[]}`)
		mu.Lock()
		answered = append(answered, time.Now())
		mu.Unlock()
	}))
	slow := &timedSource{fakeSource: fakeSource{name: "slow"}, delay: func(string) time.Duration { return 2 * time.Second }}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.session = testSession(t, srv.Transport())
	r.scanSources = []sources.Source{&sources.ProxyNova{}, slow}

	start := time.Now()
	if err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader(emailTargets(4)), []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(answered) != 4 {
		t.Fatalf("ProxyNova answered %d targets, want 4", len(answered))
	}
	last := answered[3]
	if took := last.Sub(start); took > 3*time.Second {
		t.Errorf("ProxyNova answered all four targets after %v, want within 3s", took)
	}
	started := 0
	for _, at := range slow.started() {
		if at.Before(last) {
			started++
		}
	}
	if started != 2 {
		t.Errorf("the slow source had started %d targets when ProxyNova finished, want it on its second", started)
	}
}

// timingTransport records, per host, when each request left.
type timingTransport struct {
	inner http.RoundTripper
	mu    sync.Mutex
	sent  map[string][]time.Time
}

func (tt *timingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tt.mu.Lock()
	tt.sent[req.URL.Host] = append(tt.sent[req.URL.Host], time.Now())
	tt.mu.Unlock()
	return tt.inner.RoundTrip(req)
}

// TestPipeline_NoSourceBeatsItsInterval is the B4 acceptance check over a
// 500-target list: no two requests of one source are closer than its
// interval, measured where the requests leave.
func TestPipeline_NoSourceBeatsItsInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 8 seconds")
	}
	captureLogs(t)
	captureSummary(t)
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	transport := &timingTransport{inner: srv.Transport(), sent: make(map[string][]time.Time)}
	intervals := map[string]time.Duration{"ten.example.test": 10 * time.Millisecond, "fifteen.example.test": 15 * time.Millisecond}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.session = testSession(t, transport)
	r.scanSources = []sources.Source{
		&httpSource{fakeSource: fakeSource{name: "ten"}, interval: intervals["ten.example.test"]},
		&httpSource{fakeSource: fakeSource{name: "fifteen"}, interval: intervals["fifteen.example.test"]},
	}
	if err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader(emailTargets(500)), []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	for host, interval := range intervals {
		sent := transport.sent[host]
		if len(sent) != 500 {
			t.Fatalf("%s: %d requests, want 500", host, len(sent))
		}
		for i := 1; i < len(sent); i++ {
			if gap := sent[i].Sub(sent[i-1]); gap < interval {
				t.Errorf("%s: requests %d and %d left %v apart, want at least %v", host, i-1, i, gap, interval)
			}
		}
	}
}

// TestPipeline_WritesTargetsInInputOrder: targets finish in any order but are
// written in input order, each with its own results.
func TestPipeline_WritesTargetsInInputOrder(t *testing.T) {
	captureLogs(t)
	summary := captureSummary(t)
	delays := map[string]time.Duration{"user0@example.com": 300 * time.Millisecond}
	slowFirst := &timedSource{fakeSource: fakeSource{name: "s"}, delay: func(target string) time.Duration { return delays[target] }}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Summary = true // --stats
	r.scanSources = []sources.Source{slowFirst, &timedSource{fakeSource: fakeSource{name: "t"}, delay: func(string) time.Duration { return 0 }}}
	var out bytes.Buffer
	if err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader(emailTargets(3)), []io.Writer{&out}); err != nil {
		t.Fatal(err)
	}
	want := "email:user0@example.com, password:s\nemail:user0@example.com, password:t\n" +
		"email:user1@example.com, password:s\nemail:user1@example.com, password:t\n" +
		"email:user2@example.com, password:s\nemail:user2@example.com, password:t\n"
	if out.String() != want {
		t.Errorf("output\n%s\nwant\n%s", out.String(), want)
	}
	if n := strings.Count(summary.String(), "SOURCE"); n != 3 {
		t.Errorf("%d tables, want one per target", n)
	}
}

// gatedSource blocks on target "user0@example.com" until released.
type gatedSource struct {
	fakeSource
	release chan struct{}
}

func (g *gatedSource) Search(ctx context.Context, q sources.Query, _ *sources.Session, _ func(sources.Result)) (sources.Stats, error) {
	if q.Target == "user0@example.com" {
		select {
		case <-g.release:
		case <-ctx.Done():
			return sources.Stats{Total: -1}, ctx.Err()
		}
	}
	return sources.Stats{Total: -1}, nil
}

// TestPipeline_ThreadsCapsTargetsInFlight: with -t 2, a source stuck on the
// first target lets the others reach the second target, not the third.
func TestPipeline_ThreadsCapsTargetsInFlight(t *testing.T) {
	captureLogs(t)
	captureSummary(t)
	gate := &gatedSource{fakeSource: fakeSource{name: "gated"}, release: make(chan struct{})}
	fast := &timedSource{fakeSource: fakeSource{name: "fast"}, delay: func(string) time.Duration { return 0 }}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.options.Threads = 2
	r.scanSources = []sources.Source{gate, fast}

	errc := make(chan error, 1)
	go func() {
		errc <- r.EnumerateMultipleTargets(context.Background(), strings.NewReader(emailTargets(6)), []io.Writer{io.Discard})
	}()
	time.Sleep(300 * time.Millisecond)
	if n := len(fast.started()); n != 2 {
		t.Errorf("the fast source started %d targets while the first is unfinished, want 2 (-t 2)", n)
	}
	close(gate.release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if n := len(fast.started()); n != 6 {
		t.Errorf("the fast source searched %d targets in the end, want 6", n)
	}
}

// TestPipeline_InterruptKeepsResultsFound: results already found when the
// run is interrupted are still written; the run stops with one
// "Interrupted" and the context's error.
func TestPipeline_InterruptKeepsResultsFound(t *testing.T) {
	logs := captureLogs(t)
	captureSummary(t)
	var fastDone atomic.Bool
	fast := &fakeSource{name: "fast", emits: []sources.Result{{Email: "user0@example.com", Password: "found"}},
		onStart: func() { fastDone.Store(true) }}
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.scanSources = []sources.Source{fast, &blockingSource{started: make(chan struct{})}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for !fastDone.Load() {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	var out bytes.Buffer
	err := r.EnumerateMultipleTargets(ctx, strings.NewReader("user0@example.com\n"), []io.Writer{&out})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(out.String(), "password:found") {
		t.Errorf("the result found before the interrupt was lost; output %q", out.String())
	}
	if n := strings.Count(logs.String(), "Interrupted"); n != 1 {
		t.Errorf("%d \"Interrupted\" lines, want 1:\n%s", n, logs)
	}
	if strings.Contains(logs.String(), "Found ") {
		t.Errorf("an interrupted target has no summary line:\n%s", logs)
	}
}

// TestPipeline_LogsTargetWhenASourceStartsIt: "Enumerating leaks for X" is
// logged once per target, when the first source starts it.
func TestPipeline_LogsTargetWhenASourceStartsIt(t *testing.T) {
	logs := captureLogs(t)
	captureSummary(t)
	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.scanSources = []sources.Source{&fakeSource{name: "a"}, &fakeSource{name: "b"}}
	if err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader(emailTargets(3)), []io.Writer{io.Discard}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if n := strings.Count(logs.String(), fmt.Sprintf("Enumerating leaks for user%d@example.com\n", i)); n != 1 {
			t.Errorf("user%d: %d \"Enumerating\" lines, want 1:\n%s", i, n, logs)
		}
	}
}
