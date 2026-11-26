package sources

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Kinds of source failure. The run summary reports them as the statuses
// auth, quota and rate-limited; errors.Is matches them through any wrapping.
var (
	// ErrAuth: the API refused the key (HTTP 401 or 403).
	ErrAuth = errors.New("authentication failed")
	// ErrQuota: the key has no credits or requests left (HTTP 402).
	ErrQuota = errors.New("quota exhausted")
	// ErrRateLimited: the API kept answering 429 Too Many Requests.
	ErrRateLimited = errors.New("rate limited")
)

// StatusError is an HTTP answer other than 2xx, after any retries. Body is
// one line, cut short, with every secret of the request replaced by REDACTED.
type StatusError struct {
	StatusCode int
	Host       string
	Body       string
	// RetryAfter is the wait the API asked for, 0 if it named none.
	RetryAfter time.Duration
	// Location is where a redirect pointed; redirects are not followed.
	Location string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s returned status %d", e.Host, e.StatusCode)
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %v)", e.RetryAfter)
	}
	if e.Location != "" {
		msg += fmt.Sprintf(" (redirect to %s not followed)", e.Location)
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// Unwrap maps the status to its kind: 401 and 403 are ErrAuth, 402 is
// ErrQuota and 429 is ErrRateLimited.
func (e *StatusError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuth
	case http.StatusPaymentRequired:
		return ErrQuota
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// messageError replaces the message of err and keeps err in the chain, so
// errors.Is still finds its kind.
type messageError struct {
	msg string
	err error
}

func (e *messageError) Error() string { return e.msg }

func (e *messageError) Unwrap() error { return e.err }

// withMessage returns err with the message msg.
func withMessage(err error, msg string) error {
	return &messageError{msg: msg, err: err}
}

// kindError keeps the message of an error and gives it another kind.
type kindError struct {
	msg  string
	kind error
}

func (e *kindError) Error() string { return e.msg }

func (e *kindError) Unwrap() error { return e.kind }

// withKind returns an error with the message of err whose only kind is kind,
// for APIs that use a status code for something else, such as DeHashed's
// 403 for "Insufficient Credits".
func withKind(err error, kind error) error {
	return &kindError{msg: err.Error(), kind: kind}
}
