package runner

import (
	"bufio"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Verifier enriches result values with HIBP password breach counts and hash type identification.
type Verifier struct {
	client     *http.Client
	enabled    bool
	hashCache  map[string]int      // full SHA1 hex → breach count
	rangeCache map[string][]string // 5-char prefix → suffix:count lines
	mu         sync.Mutex
	lastReq    time.Time // for HIBP rate limiting
}

// NewVerifier creates a new Verifier. When enabled is false, EnrichResult is a no-op.
func NewVerifier(enabled bool) *Verifier {
	return &Verifier{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		enabled:    enabled,
		hashCache:  make(map[string]int),
		rangeCache: make(map[string][]string),
	}
}

// EnrichResult parses a result value string and appends verification signals.
// Input format: "field1:value1, field2:value2, ..."
// Returns the enriched string (or unchanged if not enabled / no enrichable fields).
func (v *Verifier) EnrichResult(value string) string {
	if !v.enabled {
		return value
	}

	parts := splitResultFields(value)
	fields := make(map[string]string, len(parts))
	for _, p := range parts {
		idx := strings.Index(p, ":")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(p[:idx])
		val := strings.TrimSpace(p[idx+1:])
		fields[k] = val
	}

	enriched := value

	// 1a. HIBP Password Check
	if pw, ok := fields["password"]; ok && pw != "" {
		count := v.hibpCount(pw)
		enriched = enriched + fmt.Sprintf(", hibp_count:%d", count)
	}

	// 1b. Hash Format Identification
	if h, ok := fields["hash"]; ok && h != "" {
		enriched = enriched + fmt.Sprintf(", hash_type:%s", identifyHash(h))
	}

	return enriched
}

// splitResultFields splits "field1:value1, field2:value2" into individual "field:value" strings.
// It is careful not to split on colons that are part of values (e.g. timestamps).
// The format uses ", " as delimiter between fields.
func splitResultFields(value string) []string {
	return strings.Split(value, ", ")
}

// hibpCount returns the number of times the password appears in the HIBP breach corpus.
// Uses k-anonymity: only the first 5 hex characters of the SHA-1 hash are sent.
func (v *Verifier) hibpCount(password string) int {
	h := sha1.Sum([]byte(password))
	fullHash := fmt.Sprintf("%X", h[:]) // uppercase hex

	v.mu.Lock()
	if count, ok := v.hashCache[fullHash]; ok {
		v.mu.Unlock()
		return count
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
		lines, fetchErr = v.fetchHIBPRange(prefix)
		v.mu.Lock()
		v.lastReq = time.Now()
		if fetchErr == nil {
			v.rangeCache[prefix] = lines
		}
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
			n := 0
			fmt.Sscanf(line[colonIdx+1:], "%d", &n)
			count = n
			break
		}
	}

	v.mu.Lock()
	v.hashCache[fullHash] = count
	v.mu.Unlock()

	return count
}

// fetchHIBPRange queries the HIBP Passwords API for the given 5-char SHA-1 prefix.
func (v *Verifier) fetchHIBPRange(prefix string) ([]string, error) {
	url := fmt.Sprintf("https://api.haveibeenpwned.com/range/%s", prefix)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Add-Padding", "true")
	req.Header.Set("User-Agent", "leaker")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HIBP API returned status %d", resp.StatusCode)
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return lines, err
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
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
