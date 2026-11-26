package sources

import "testing"

type matchCase struct {
	result Result
	want   bool
}

func checkMatches(t *testing.T, st ScanType, target string, cases []matchCase) {
	t.Helper()
	for _, c := range cases {
		if got := c.result.Matches(st, target); got != c.want {
			t.Errorf("Matches(%q) on %+v = %v, want %v", target, c.result, got, c.want)
		}
	}
}

// TestMatches_DomainFourEmails keeps the exact domain and its subdomains and
// drops look-alikes that merely contain the domain as a substring.
func TestMatches_DomainFourEmails(t *testing.T) {
	checkMatches(t, TypeDomain, "example.com", []matchCase{
		{Result{Email: "a@example.com"}, true},
		{Result{Email: "x@sub.example.com"}, true},
		{Result{Email: "a@notexample.com"}, false},
		{Result{Email: "b@example.com.evil.org"}, false},
	})
}

func TestMatches_DomainURLHostAndUsername(t *testing.T) {
	checkMatches(t, TypeDomain, "Example.com", []matchCase{
		{Result{URL: "https://login.example.com/signin?next=/"}, true},
		{Result{URL: "login.example.com/signin"}, true},
		{Result{URL: "https://user:pw@EXAMPLE.COM:8443/x"}, true},
		{Result{URL: "https://example.com.evil.org/"}, false},
		{Result{URL: "https://evil.org/?u=example.com"}, false},
		{Result{Username: "bob@example.com", URL: "https://social.test/login"}, true},
		{Result{Username: "example.com", Password: "example.com"}, false},
		{Result{Database: "Example.com breach"}, false},
	})
}

// TestMatches_PhoneFormats compares phone numbers by digits, so formatting,
// a missing country code or a trunk prefix do not hide a record.
func TestMatches_PhoneFormats(t *testing.T) {
	checkMatches(t, TypePhone, "15552341096", []matchCase{
		{Result{Phone: "15552341096"}, true},
		{Result{Phone: "+1 (555) 234-1096"}, true},
		{Result{Phone: "555.234.1096"}, true},
		{Result{Username: "15552341096"}, true},
		{Result{Email: "15552341096@sms.example.com"}, true},
		{Result{Phone: "+1 (555) 234-1097"}, false},
		{Result{Phone: "1096"}, false},
		{Result{Password: "15552341096"}, false},
	})
	checkMatches(t, TypePhone, "+7 (999) 123-45-67", []matchCase{
		{Result{Phone: "8 999 123 45 67"}, true},
		{Result{Phone: "79991234567"}, true},
		{Result{Phone: "79991234568"}, false},
	})
}

func TestMatches_Email(t *testing.T) {
	checkMatches(t, TypeEmail, "jane@example.com", []matchCase{
		{Result{Email: "JANE@example.com"}, true},
		{Result{Email: "xjane@example.com"}, false},
		{Result{Email: "jane@example.com.au"}, false},
		{Result{Email: "other@example.com", Username: "jane@example.com"}, true},
		{Result{URL: "https://site.test/?u=jane@example.com"}, true},
	})
}

func TestMatches_UsernameAndKeywordAreSubstring(t *testing.T) {
	checkMatches(t, TypeUsername, "admin", []matchCase{
		{Result{Username: "SuperAdmin"}, true},
		{Result{Email: "admin@example.com"}, true},
		{Result{Username: "root"}, false},
	})
	checkMatches(t, TypeKeyword, "acme corp", []matchCase{
		{Result{Database: "ACME Corp 2020"}, true},
		{Result{Database: "acme"}, false},
	})
}

// TestMatches_PhoneTrunkZero: national formats with a trunk 0 before a short
// national number match their international form.
func TestMatches_PhoneTrunkZero(t *testing.T) {
	cases := []struct {
		target, phone string
		want          bool
	}{
		{"33612345678", "06 12 34 56 78", true},       // France
		{"33612345678", "+33 (0)6 12 34 56 78", true}, // France, written with (0)
		{"31612345678", "06-12345678", true},          // Netherlands
		{"61412345678", "0412 345 678", true},         // Australia
		{"972501234567", "050-123-4567", true},        // Israel
		{"3221234567", "02 123 45 67", true},          // Belgium, 8-digit national number
		{"15552341096", "6552341096", false},          // US: another area code
		{"33612345678", "07 12 34 56 78", false},      // another French number
	}
	for _, c := range cases {
		if got := (&Result{Phone: c.phone}).Matches(TypePhone, c.target); got != c.want {
			t.Errorf("phone %q vs target %q: Matches = %v, want %v", c.phone, c.target, got, c.want)
		}
	}
}
