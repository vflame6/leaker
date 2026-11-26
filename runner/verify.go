package runner

import (
	"bufio"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vflame6/leaker/runner/sources"
)

// Verifier enriches results with HIBP password breach counts and hash type
// identification. One Verifier serves a whole run, so its caches span
// targets, and it sends requests through the run's session (proxy included).
type Verifier struct {
	session    *sources.Session
	enabled    bool
	hashCache  map[string]int      // full SHA1 hex → breach count
	rangeCache map[string][]string // 5-char prefix → suffix:count lines
	mu         sync.Mutex
	lastReq    time.Time // for HIBP rate limiting
}

// NewVerifier creates a new Verifier. When enabled is false, EnrichResult is a no-op.
func NewVerifier(enabled bool, session *sources.Session) *Verifier {
	return &Verifier{
		session:    session,
		enabled:    enabled,
		hashCache:  make(map[string]int),
		rangeCache: make(map[string][]string),
	}
}

// EnrichResult enriches a Result in-place with verification signals. A failed
// HIBP lookup sets hibp_error instead of hibp_count, so it never reads as
// "not breached".
func (v *Verifier) EnrichResult(ctx context.Context, result *sources.Result) {
	if v == nil || !v.enabled {
		return
	}

	// HIBP Password Check
	if result.Password != "" {
		count, err := v.hibpCount(ctx, result.Password)
		if err != nil {
			result.SetExtra("hibp_error", err.Error())
		} else {
			result.SetExtra("hibp_count", strconv.Itoa(count))
		}
	}

	// Hash Format Identification
	if result.Hash != "" {
		result.SetExtra("hash_type", identifyHash(result.Hash))
	}
}

// hibpCount returns the number of times the password appears in the HIBP breach corpus.
// Uses k-anonymity: only the first 5 hex characters of the SHA-1 hash are sent.
// Failed lookups are not cached.
func (v *Verifier) hibpCount(ctx context.Context, password string) (int, error) {
	h := sha1.Sum([]byte(password))
	fullHash := fmt.Sprintf("%X", h[:]) // uppercase hex

	v.mu.Lock()
	if count, ok := v.hashCache[fullHash]; ok {
		v.mu.Unlock()
		return count, nil
	}
	v.mu.Unlock()

	prefix := fullHash[:5]
	suffix := fullHash[5:]

	// Rate limit: max 1 request per 100ms
	v.mu.Lock()
	since := time.Since(v.lastReq)
	if since < 100*time.Millisecond {
		time.Sleep(100*time.Millisecond - since)
	}

	// Check range cache while holding lock
	lines, rangeHit := v.rangeCache[prefix]
	if !rangeHit {
		v.mu.Unlock()
		// Fetch from HIBP
		var fetchErr error
		lines, fetchErr = v.fetchHIBPRange(ctx, prefix)
		v.mu.Lock()
		v.lastReq = time.Now()
		if fetchErr != nil {
			v.mu.Unlock()
			return 0, fetchErr
		}
		v.rangeCache[prefix] = lines
	}
	v.mu.Unlock()

	count := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 35 {
			continue
		}
		// Format: SUFFIX:COUNT (suffix is 35 chars for SHA-1)
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		lineSuffix := line[:colonIdx]
		if strings.EqualFold(lineSuffix, suffix) {
			n, _ := strconv.Atoi(strings.TrimSpace(line[colonIdx+1:]))
			count = n
			break
		}
	}

	v.mu.Lock()
	v.hashCache[fullHash] = count
	v.mu.Unlock()

	return count, nil
}

// fetchHIBPRange queries the HIBP Passwords API for the given 5-char SHA-1 prefix.
func (v *Verifier) fetchHIBPRange(ctx context.Context, prefix string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.pwnedpasswords.com/range/"+prefix, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Add-Padding", "true")

	resp, err := v.session.Do(req)
	if err != nil {
		// Keep the cause; the URL only repeats the hash prefix.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, fmt.Errorf("HIBP lookup failed: %w", urlErr.Err)
		}
		return nil, fmt.Errorf("HIBP lookup failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HIBP API returned status %d", resp.StatusCode)
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("HIBP response could not be read: %w", err)
	}
	return lines, nil
}

// identifyHash returns the hash algorithm name based on the hash string's format.
func identifyHash(hash string) string {
	// Modular crypt formats
	if matched, _ := regexp.MatchString(`^\$2[aby]\$`, hash); matched {
		return "bcrypt"
	}
	if strings.HasPrefix(hash, "$1$") {
		return "md5crypt"
	}
	if strings.HasPrefix(hash, "$5$") {
		return "sha256crypt"
	}
	if strings.HasPrefix(hash, "$6$") {
		return "sha512crypt"
	}
	if strings.HasPrefix(hash, "$argon2") {
		return "argon2"
	}

	// Raw hex hashes
	if isHex(hash) {
		switch len(hash) {
		case 32:
			return "md5"
		case 40:
			return "sha1"
		case 64:
			return "sha256"
		case 128:
			return "sha512"
		}
	}

	return "unknown"
}

// isHex returns true if s contains only hexadecimal characters.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range strings.ToLower(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
