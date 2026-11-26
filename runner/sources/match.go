package sources

import (
	"net"
	"strings"

	"github.com/vflame6/leaker/utils"
)

// Phone numbers are compared by their trailing digits: the last
// min(len(a), len(b), phoneMatchDigits) digits must be equal, and at least
// phoneMinDigits of them. "+1 (555) 234-1096", "15552341096" and
// "5552341096" match each other, and so do "+7 999 123-45-67" and
// "8 999 123 45 67" (national trunk prefix).
//
// A national number written with its trunk prefix 0 is shorter than its
// international form: "06 12 34 56 78" is +33 6 12 34 56 78. When a 0 comes
// right before the compared digits, down to phoneTrunkMinDigits are
// compared, so French, Dutch, Australian or Belgian numbers match too.
const (
	phoneMatchDigits    = 10
	phoneMinDigits      = 7
	phoneTrunkMinDigits = 8
)

// Matches reports whether the result belongs to target for the given scan
// type. It is the default result filter (--no-filter disables it).
//
//   - email: the email field equals the target; other fields may contain it.
//   - domain: an email domain (from the email field, or a username that is an
//     email) or the URL host is the target or one of its subdomains.
//   - phone: the phone, username or email field holds the same number,
//     compared by digits.
//   - username, keyword: any field contains the target.
//
// All comparisons ignore case.
func (r *Result) Matches(scanType ScanType, target string) bool {
	switch scanType {
	case TypeEmail:
		return strings.EqualFold(r.Email, strings.TrimSpace(target)) ||
			anyContains(r.fieldsExceptEmail(), target)
	case TypeDomain:
		return r.matchesDomain(target)
	case TypePhone:
		return r.matchesPhone(target)
	default:
		return anyContains(append(r.fieldsExceptEmail(), r.Email), target)
	}
}

func (r *Result) fieldsExceptEmail() []string {
	return []string{r.Username, r.Password, r.Hash, r.Salt, r.IP, r.Phone, r.Name, r.Database, r.URL}
}

func anyContains(fields []string, target string) bool {
	t := strings.ToLower(target)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), t) {
			return true
		}
	}
	return false
}

func (r *Result) matchesDomain(target string) bool {
	domain := normalizeHost(target)
	if domain == "" {
		return false
	}
	for _, v := range []string{r.Email, r.Username} {
		if at := strings.LastIndex(v, "@"); at >= 0 && withinDomain(normalizeHost(v[at+1:]), domain) {
			return true
		}
	}
	return withinDomain(urlHost(r.URL), domain)
}

// withinDomain reports whether host is domain or one of its subdomains.
func withinDomain(host, domain string) bool {
	return host != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

// urlHost returns the host of a URL-like value, with or without a scheme,
// userinfo or port: "https://u:p@Login.Example.com:8443/x" → "login.example.com".
func urlHost(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+len("://"):]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	return normalizeHost(s)
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.Trim(h, "[]")
	return strings.TrimRight(h, ".")
}

func (r *Result) matchesPhone(target string) bool {
	digits := utils.ExtractPhoneDigits(target)
	for _, v := range []string{r.Phone, r.Username, r.Email} {
		if samePhone(utils.ExtractPhoneDigits(v), digits) {
			return true
		}
	}
	return false
}

func samePhone(a, b string) bool {
	n := min(len(a), len(b), phoneMatchDigits)
	if n < phoneMinDigits {
		return false
	}
	if a[len(a)-n:] == b[len(b)-n:] {
		return true
	}
	for k := n - 1; k >= phoneTrunkMinDigits; k-- {
		if (trunkZeroBefore(a, k) || trunkZeroBefore(b, k)) && a[len(a)-k:] == b[len(b)-k:] {
			return true
		}
	}
	return false
}

// trunkZeroBefore reports whether a 0 precedes the last k digits.
func trunkZeroBefore(digits string, k int) bool {
	return len(digits) > k && digits[len(digits)-k-1] == '0'
}
