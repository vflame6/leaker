package sources

// Limits are what one search of a source's API can return.
type Limits struct {
	// PageSize is the number of records one request returns at most; 0 when
	// one request returns everything or the API does not say.
	PageSize int
	// MaxResults is the number of records one search returns at most, over
	// all its pages; 0 when the API does not say.
	MaxResults int
	// DefaultRequests replaces the run's default --max-requests for this
	// source when the user gives none; 0 keeps the run's default.
	DefaultRequests int
}

// pager applies the limits of a query to a paged search:
//
//	p := newPager(q, s.Limits())
//	for page := 1; ; page++ {
//		records := fetch(page, p.size())
//		for _, record := range records[:p.take(len(records))] {
//			emit(...)
//		}
//		if !p.next(len(records), total, hasNextPage) {
//			break
//		}
//	}
//	return p.stats(), nil
//
// The search ends when every record is read, a page comes back empty, the
// API has no further page, or a cap is reached: --max-results, the API's own
// cap or --max-requests. Ending at a cap with records left is a truncation.
// So that no API can keep a search going forever (with --all there is no
// --max-requests), ten empty pages in a row, a repeated cursor or page (see
// repeated) and MaxPagesWithoutLimit pages end it too.
type pager struct {
	q        Query
	limits   Limits
	pageSize int
	pages    int
	fetched  int
	// dropped counts records received beyond the cap.
	dropped   int
	total     int
	truncated bool
	reason    Limit
	// empty counts empty pages in a row; seen holds cursors and page
	// fingerprints (see repeated).
	empty int
	seen  map[string]struct{}
}

func newPager(q Query, limits Limits) *pager {
	p := &pager{q: q, limits: limits, total: -1}
	// One page size for every page: page-numbered APIs need it constant.
	p.pageSize = limits.PageSize
	if want := p.want(); want >= 0 && (p.pageSize == 0 || want < p.pageSize) {
		p.pageSize = want
	}
	return p
}

// limit is the most records the search may take; 0 means no cap.
func (p *pager) limit() int {
	limit := p.q.MaxResults
	if apiCap := p.limits.MaxResults; apiCap > 0 && (limit == 0 || apiCap < limit) {
		limit = apiCap
	}
	return limit
}

// want is how many more records the search may take; -1 means no cap.
func (p *pager) want() int {
	limit := p.limit()
	if limit == 0 {
		return -1
	}
	return max(limit-p.fetched, 0)
}

// size is the number of records to ask for per page; 0 means the API's
// default.
func (p *pager) size() int {
	return p.pageSize
}

// take returns how many of the n records just received the search keeps,
// and counts them as fetched.
func (p *pager) take(n int) int {
	if want := p.want(); want >= 0 && n > want {
		p.dropped += n - want
		n = want
	}
	p.fetched += n
	return n
}

// next records a page of got records: total is the number of records the
// API says exist (-1 if it does not say) and more whether there is a next
// page. APIs that say so (a cursor, has_next) can send empty pages that are
// not the last; for the others more is "this page was full", so an empty
// page ends the search. It reports whether to request the next page.
func (p *pager) next(got, total int, more bool) bool {
	p.pages++
	if total >= 0 {
		p.total = total
	}
	if got == 0 {
		p.empty++
	} else {
		p.empty = 0
	}
	if p.dropped == 0 {
		if p.total >= 0 && p.fetched >= p.total {
			return false
		}
		if p.total < 0 && !more {
			return false
		}
	}
	// Records are left behind if the search stops here.
	switch {
	case p.limits.MaxResults > 0 && p.fetched >= p.limits.MaxResults:
		p.truncate(LimitAPI)
	case p.q.MaxResults > 0 && p.fetched >= p.q.MaxResults:
		p.truncate(LimitResults)
	case !more || (got == 0 && p.total >= 0):
		// The API says more exist but serves no further page.
		p.truncate(LimitAPI)
	case p.q.MaxRequests > 0 && p.pages >= p.q.MaxRequests:
		p.truncate(LimitRequests)
	case p.empty >= maxEmptyPages:
		// The API keeps saying there is more but sends nothing.
		p.truncate(LimitAPI)
	case p.q.MaxRequests == 0 && p.pages >= MaxPagesWithoutLimit:
		p.truncate(LimitSafety)
	default:
		return true
	}
	return false
}

// MaxPagesWithoutLimit caps the pages of a search without --max-requests.
const MaxPagesWithoutLimit = 1000

// maxEmptyPages is how many empty pages in a row end a search.
const maxEmptyPages = 10

// repeated reports whether a cursor, or the fingerprint of a page, was seen
// before in this search: an API that repeats itself would page forever. A
// repeat ends the search as truncated.
func (p *pager) repeated(key string) bool {
	if p.seen == nil {
		p.seen = make(map[string]struct{})
	}
	if _, ok := p.seen[key]; ok {
		p.truncate(LimitAPI)
		return true
	}
	p.seen[key] = struct{}{}
	return false
}

func (p *pager) truncate(reason Limit) {
	p.truncated, p.reason = true, reason
}

// stats returns what the search fetched and why it stopped.
func (p *pager) stats() Stats {
	return Stats{Total: p.total, Fetched: p.fetched, Truncated: p.truncated, Limit: p.reason}
}
