package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
	"github.com/vflame6/leaker/runner/sources"
)

// proxyNovaRun searches n targets with the real ProxyNova source against a
// fake API and returns the time of each request.
func proxyNovaRun(t *testing.T, n int) []time.Time {
	t.Helper()
	captureLogs(t)
	var mu sync.Mutex
	var times []time.Time
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		_, _ = io.WriteString(w, `{"count":0,"lines":[]}`)
	}))

	r := newTestRunner(nil)
	r.options.Type = sources.TypeEmail
	r.session = testSession(t, srv.Transport())
	r.scanSources = []sources.Source{&sources.ProxyNova{}}

	var input strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&input, "user%d@example.com\n", i)
	}
	if err := r.EnumerateMultipleTargets(context.Background(), strings.NewReader(input.String()), []io.Writer{&bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != n {
		t.Fatalf("%d requests, want %d", len(times), n)
	}
	return times
}

// TestProxyNova_IntervalBetweenTargets: consecutive ProxyNova requests are at
// least MinInterval (667ms) apart.
func TestProxyNova_IntervalBetweenTargets(t *testing.T) {
	times := proxyNovaRun(t, 3)
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 660*time.Millisecond {
			t.Errorf("requests %d and %d were %v apart, want at least 667ms", i-1, i, gap)
		}
	}
}

// TestProxyNova_SixtyTargetsTakeThirtyNineSeconds is the v1.8 acceptance
// check: about 90 requests per minute, so 60 targets take at least 39s.
func TestProxyNova_SixtyTargetsTakeThirtyNineSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 40 seconds")
	}
	start := time.Now()
	proxyNovaRun(t, 60)
	if elapsed := time.Since(start); elapsed < 39*time.Second {
		t.Errorf("60 targets took %v, want at least 39s", elapsed)
	}
}
