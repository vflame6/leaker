package sources

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vflame6/leaker/logger"
	"golang.org/x/time/rate"
)

// SessionOptions configures NewSession.
type SessionOptions struct {
	Timeout   time.Duration
	UserAgent string
	// Proxy is the --proxy URL. When empty, the HTTP_PROXY, HTTPS_PROXY and
	// NO_PROXY environment variables decide, as for any Go program.
	Proxy string
	// Insecure disables TLS certificate verification (--insecure).
	Insecure bool
	// Transport replaces the network transport, for tests. Proxy and Insecure
	// do not apply to it.
	Transport http.RoundTripper
	// Retry sets how Do retries; the zero value is three retries starting at
	// one second.
	Retry RetryPolicy
}

// RetryPolicy configures how Session.Do retries 429, 502, 503 and 504.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first try (0 → 3).
	MaxRetries int
	// BaseDelay is the first backoff; it doubles on every retry (0 → 1s).
	BaseDelay time.Duration
	// MaxDelay caps the backoff and the Retry-After Do is willing to wait;
	// an API asking for longer is not retried (0 → 30s).
	MaxDelay time.Duration
}

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.MaxRetries <= 0 {
		p.MaxRetries = 3
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = time.Second
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 30 * time.Second
	}
	return p
}

// NewSession creates the HTTP session shared by every source during a run.
// An invalid proxy is an error, so traffic never silently bypasses it.
func NewSession(opts SessionOptions) (*Session, error) {
	transport := opts.Transport
	if transport == nil {
		tr := &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: opts.Insecure, //nolint:gosec // controlled by --insecure flag
			},
			DialContext: (&net.Dialer{
				Timeout: opts.Timeout,
			}).DialContext,
		}
		if opts.Proxy != "" {
			proxyURL, err := parseProxy(opts.Proxy)
			if err != nil {
				return nil, err
			}
			tr.Proxy = http.ProxyURL(proxyURL)
		}
		transport = tr
	}

	client := &http.Client{
		Transport: &CustomTransport{
			Transport: transport,
			UserAgent: opts.UserAgent,
		},
		Timeout: opts.Timeout,
		// No source relies on a redirect, and a followed one would carry the
		// custom key headers (X-API-Key, Dehashed-Api-Key, ...) to whatever
		// host it names: Do turns a redirect into an error instead.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Session{client: client, retry: opts.Retry.withDefaults(), sent: new(atomic.Int64)}, nil
}

// ForSource returns a handle on the session for one source: it shares the
// connections and the retry policy, waits at least interval between two of
// its requests (no wait when interval is 0) and counts its own requests.
func (s *Session) ForSource(interval time.Duration) *Session {
	handle := &Session{client: s.client, retry: s.retry, sent: new(atomic.Int64)}
	if interval > 0 {
		handle.pace = &pacer{limiter: rate.NewLimiter(rate.Every(interval), 1), interval: interval}
	}
	return handle
}

// pacer spaces the requests of one source by at least interval. The
// rate.Limiter spaces its reservations; a request that leaves late would
// still shorten the gap to the next one, so the time the last request left
// is kept too, with paceMargin on top for the HTTP client's own overhead.
type pacer struct {
	limiter  *rate.Limiter
	interval time.Duration

	mu   sync.Mutex
	last time.Time
}

// paceMargin covers the time between the pacer and the network, which
// varies from one request to the next.
const paceMargin = time.Millisecond

// wait blocks until the next request may leave, or ctx is done.
func (p *pacer) wait(ctx context.Context) error {
	if err := p.limiter.Wait(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.last.IsZero() {
		if early := p.interval + paceMargin - time.Since(p.last); early > 0 {
			timer := time.NewTimer(early)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	p.last = time.Now()
	return nil
}

// Requests returns the number of HTTP requests sent through this handle,
// retries included.
func (s *Session) Requests() int {
	return int(s.sent.Load())
}

// parseProxy validates a --proxy value. Error messages never include a
// password from the URL.
func parseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("invalid proxy: use a URL such as http://127.0.0.1:8080")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	default:
		return nil, fmt.Errorf("invalid proxy %s: the scheme must be http, https, socks5 or socks5h", u.Redacted())
	}
}

// secretParams matches token, api_key, apikey and key parameters, in a URL
// or in an echoed form body.
var secretParams = regexp.MustCompile(`(?i)((?:^|[?&\s"'{,;(])(?:token|api_?key|key)=)[^&#\s"']*`)

// secretFieldsJSON matches JSON fields of those names.
var secretFieldsJSON = regexp.MustCompile(`(?i)("(?:token|api_?key|key)"\s*:\s*")(?:[^"\\]|\\.)*"`)

// RedactSecrets replaces the values of the token, api_key, apikey and key
// parameters and JSON fields in s with REDACTED. s may be a URL or any text
// containing URLs, such as an error message.
func RedactSecrets(s string) string {
	s = secretParams.ReplaceAllString(s, "${1}REDACTED")
	return secretFieldsJSON.ReplaceAllString(s, `${1}REDACTED"`)
}

// secretForms returns the ways key can appear in an echoed request: as is,
// URL- and form-encoded, and JSON-escaped (also with "\/", as PHP writes).
func secretForms(key string) []string {
	forms := []string{key, url.QueryEscape(key), url.PathEscape(key)}
	if quoted, err := json.Marshal(key); err == nil {
		escaped := string(quoted[1 : len(quoted)-1])
		forms = append(forms, escaped, strings.ReplaceAll(escaped, "/", `\/`))
	}
	// Longest first, so no form is cut by a shorter one.
	slices.SortFunc(forms, func(a, b string) int { return len(b) - len(a) })
	return slices.Compact(forms)
}

const (
	// errorBodyLimit caps how much of an API error response an error quotes.
	errorBodyLimit = 300
	// minRedactedKeyLength skips replacing implausibly short "keys", which
	// would only garble the message.
	minRedactedKeyLength = 4
)

// errorBody prepares an API error response for an error message. Some
// services, and proxy block pages, echo the request back, so every given
// key is replaced with REDACTED wherever it appears, in any of its encoded
// forms, secret parameters are redacted as in RedactSecrets, and the text is
// put on one line and cut to errorBodyLimit bytes.
func errorBody(body []byte, keys ...string) string {
	s := string(body)
	for _, key := range keys {
		if len(key) < minRedactedKeyLength {
			continue
		}
		for _, form := range secretForms(key) {
			s = strings.ReplaceAll(s, form, "REDACTED")
		}
	}
	s = strings.Join(strings.Fields(RedactSecrets(s)), " ")
	if len(s) > errorBodyLimit {
		cut := errorBodyLimit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

// Do sends req and returns a 2xx response, whose body the caller closes.
// Sources must send every request through Do. In order, it
//
//   - waits on the source's limiter (see ForSource),
//   - sends the request,
//   - retries 429, 502, 503 and 504 up to RetryPolicy.MaxRetries times, with
//     exponential backoff and jitter or the wait the API asks for in
//     Retry-After (never 401, 402 or 403),
//   - turns any other answer into a *StatusError and keeps API keys out of
//     it, of transport errors and of debug logs,
//   - counts every request it sends.
func (s *Session) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		attemptReq := req
		if attempt > 0 {
			attemptReq = req.Clone(req.Context())
			if req.GetBody != nil {
				// The first try consumed the body.
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				attemptReq.Body = body
			}
		}

		logger.Debugf("%s %s", req.Method, RedactSecrets(req.URL.Redacted()))
		// Wait as close to sending as possible.
		if s.pace != nil {
			if err := s.pace.wait(ctx); err != nil {
				return nil, err
			}
		}
		s.sent.Add(1)
		resp, err := s.client.Do(attemptReq)
		if err != nil {
			// http.Client puts the full request URL into its *url.Error.
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				urlErr.URL = RedactSecrets(urlErr.URL)
			}
			return nil, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		statusErr := newStatusError(req, resp)
		wait, retry := s.retryWait(attempt, statusErr)
		if !retry || (req.Body != nil && req.GetBody == nil) {
			return nil, statusErr
		}
		logger.Debugf("%s returned status %d; retry %d of %d in %v",
			statusErr.Host, statusErr.StatusCode, attempt+1, s.retry.MaxRetries, wait.Round(time.Millisecond))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// retryWait reports whether to retry after the attempt-th try (from 0)
// failed with e, and how long to wait first.
func (s *Session) retryWait(attempt int, e *StatusError) (time.Duration, bool) {
	switch e.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
	default:
		return 0, false
	}
	if attempt >= s.retry.MaxRetries {
		return 0, false
	}
	if e.RetryAfter > 0 {
		// An API asking for a long pause will not answer sooner.
		return e.RetryAfter, e.RetryAfter <= s.retry.MaxDelay
	}
	return backoff(s.retry, attempt), true
}

// backoff doubles BaseDelay on every retry, up to MaxDelay, and keeps a
// random half of it so that clients spread out.
func backoff(p RetryPolicy, attempt int) time.Duration {
	delay := p.MaxDelay
	if attempt < 20 && p.BaseDelay<<attempt < p.MaxDelay {
		delay = p.BaseDelay << attempt
	}
	half := delay / 2
	return half + rand.N(delay-half+1)
}

// newStatusError reads the start of a failed response and closes it.
func newStatusError(req *http.Request, resp *http.Response) *StatusError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, statusBodyLimit))
	// Drain a little more so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, statusBodyLimit))
	_ = resp.Body.Close()
	statusErr := &StatusError{
		StatusCode: resp.StatusCode,
		Host:       req.URL.Hostname(),
		Body:       errorBody(body, requestSecrets(req)...),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		statusErr.Location = errorBody([]byte(resp.Header.Get("Location")), requestSecrets(req)...)
	}
	return statusErr
}

// statusBodyLimit caps how much of a failed response is read.
const statusBodyLimit = 64 << 10

// secretHeader matches the names of headers that carry API keys.
var secretHeader = regexp.MustCompile(`(?i)auth|key|token|secret`)

// secretFields are the query, form and JSON fields that carry API keys.
var secretFields = map[string]bool{"token": true, "api_key": true, "apikey": true, "key": true}

// requestSecrets returns the API keys req carries: in headers such as
// Authorization or X-API-Key, in the query and in JSON or form bodies.
func requestSecrets(req *http.Request) []string {
	var secrets []string
	for name, values := range req.Header {
		if !secretHeader.MatchString(name) {
			continue
		}
		for _, v := range values {
			secrets = append(secrets, v)
			// "Bearer TOKEN": the token alone may be echoed too.
			if _, token, ok := strings.Cut(v, " "); ok {
				secrets = append(secrets, strings.TrimSpace(token))
			}
		}
	}
	for name, values := range req.URL.Query() {
		if secretFields[strings.ToLower(name)] {
			secrets = append(secrets, values...)
		}
	}
	if req.GetBody == nil {
		return secrets
	}
	body, err := req.GetBody()
	if err != nil {
		return secrets
	}
	data, _ := io.ReadAll(io.LimitReader(body, statusBodyLimit))
	_ = body.Close()
	switch contentType := req.Header.Get("Content-Type"); {
	case strings.Contains(contentType, "json"):
		var fields map[string]any
		if json.Unmarshal(data, &fields) == nil {
			for name, v := range fields {
				if value, ok := v.(string); ok && secretFields[strings.ToLower(name)] {
					secrets = append(secrets, value)
				}
			}
		}
	case strings.Contains(contentType, "x-www-form-urlencoded"):
		if form, err := url.ParseQuery(string(data)); err == nil {
			for name, values := range form {
				if secretFields[strings.ToLower(name)] {
					secrets = append(secrets, values...)
				}
			}
		}
	}
	return secrets
}

// DiscardHTTPResponse discards the response content by demand
func (s *Session) DiscardHTTPResponse(response *http.Response) {
	if response != nil {
		_, err := io.Copy(io.Discard, response.Body)
		if err != nil {
			logger.Errorf("Could not discard response body: %s\n", err)
			return
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			logger.Errorf("Could not close response body: %s\n", closeErr)
		}
	}
}

// Close the session
func (s *Session) Close() {
	s.client.CloseIdleConnections()
}

// RoundTrip implements the http.RoundTripper interface.
// custom one is needed to specify user agent string.
func (t *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Set the User-Agent header on the request.
	req.Header.Set("User-Agent", t.UserAgent)
	// set other headers
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Use the underlying transport to perform the actual request.
	return t.Transport.RoundTrip(req)
}

// parseRetryAfter reads a Retry-After header: seconds or an HTTP date.
// It returns 0 when there is no usable wait.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if wait := when.Sub(now); wait > 0 {
			return wait
		}
	}
	return 0
}
