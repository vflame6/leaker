package sources

import "testing"

// page is one answer of a paged API in the pager tests.
type page struct {
	got, total int
	more       bool
}

func TestPager(t *testing.T) {
	cases := []struct {
		name   string
		q      Query
		limits Limits
		pages  []page
		// wantPages is how many pages are read; the rest are not requested.
		wantPages   int
		wantFetched int
		wantSize    int
		wantLimit   Limit // LimitNone: not truncated
	}{
		{name: "all read when the total is reached", q: Query{MaxResults: 1000, MaxRequests: 3}, limits: Limits{PageSize: 100},
			pages: []page{{100, 250, true}, {100, 250, true}, {50, 250, false}}, wantPages: 3, wantFetched: 250, wantSize: 100},
		{name: "an empty page ends a search without a total", q: Query{MaxRequests: 5}, limits: Limits{PageSize: 100},
			pages: []page{{100, -1, true}, {0, -1, false}, {100, -1, false}}, wantPages: 2, wantFetched: 100, wantSize: 100},
		{name: "an empty page the API says is not the last is skipped", q: Query{MaxRequests: 5}, limits: Limits{PageSize: 100},
			pages: []page{{0, -1, true}, {5, -1, false}}, wantPages: 2, wantFetched: 5, wantSize: 100},
		{name: "an empty page short of the total is the API's limit", q: Query{MaxRequests: 5}, limits: Limits{PageSize: 100},
			pages: []page{{100, 250, true}, {0, 250, true}, {100, 250, true}}, wantPages: 2, wantFetched: 100, wantSize: 100, wantLimit: LimitAPI},
		{name: "no next page ends the search", q: Query{}, limits: Limits{PageSize: 100},
			pages: []page{{30, -1, false}}, wantPages: 1, wantFetched: 30, wantSize: 100},
		{name: "max-results stops and trims", q: Query{MaxResults: 150, MaxRequests: 5}, limits: Limits{PageSize: 100},
			pages: []page{{100, 4213, true}, {100, 4213, true}}, wantPages: 2, wantFetched: 150, wantSize: 100, wantLimit: LimitResults},
		{name: "page size shrinks to what is wanted", q: Query{MaxResults: 1000, MaxRequests: 3}, limits: Limits{PageSize: 10000, MaxResults: 10000},
			pages: []page{{1000, 4213, true}}, wantPages: 1, wantFetched: 1000, wantSize: 1000, wantLimit: LimitResults},
		{name: "no limit takes everything", q: Query{}, limits: Limits{PageSize: 10000, MaxResults: 10000},
			pages: []page{{4213, 4213, false}}, wantPages: 1, wantFetched: 4213, wantSize: 10000},
		{name: "the API cap wins over a larger max-results", q: Query{MaxResults: 20000}, limits: Limits{PageSize: 10000, MaxResults: 10000},
			pages: []page{{10000, 52341, true}}, wantPages: 1, wantFetched: 10000, wantSize: 10000, wantLimit: LimitAPI},
		{name: "max-requests stops paging", q: Query{MaxResults: 1000, MaxRequests: 2}, limits: Limits{PageSize: 100},
			pages: []page{{100, 4213, true}, {100, 4213, true}}, wantPages: 2, wantFetched: 200, wantSize: 100, wantLimit: LimitRequests},
		{name: "a total beyond the last page is the API's limit", q: Query{MaxResults: 1000}, limits: Limits{PageSize: 100, MaxResults: 100},
			pages: []page{{100, 523, false}}, wantPages: 1, wantFetched: 100, wantSize: 100, wantLimit: LimitAPI},
		{name: "a single answer trimmed by max-results", q: Query{MaxResults: 1000}, limits: Limits{},
			pages: []page{{1500, -1, false}}, wantPages: 1, wantFetched: 1000, wantSize: 1000, wantLimit: LimitResults},
		{name: "unknown page size asks for what is wanted", q: Query{MaxResults: 50}, limits: Limits{},
			pages: []page{{20, -1, false}}, wantPages: 1, wantFetched: 20, wantSize: 50},
		{name: "unknown page size and no limit", q: Query{}, limits: Limits{},
			pages: []page{{20, -1, false}}, wantPages: 1, wantFetched: 20, wantSize: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPager(c.q, c.limits)
			if got := p.size(); got != c.wantSize {
				t.Errorf("size() = %d, want %d", got, c.wantSize)
			}
			read := 0
			for _, pg := range c.pages {
				read++
				p.take(pg.got)
				if !p.next(pg.got, pg.total, pg.more) {
					break
				}
			}
			if read != c.wantPages {
				t.Errorf("read %d pages, want %d", read, c.wantPages)
			}
			stats := p.stats()
			if p.fetched != c.wantFetched {
				t.Errorf("fetched %d, want %d", p.fetched, c.wantFetched)
			}
			if stats.Truncated != (c.wantLimit != LimitNone) || stats.Limit != c.wantLimit {
				t.Errorf("truncated %v limit %v, want limit %v", stats.Truncated, stats.Limit, c.wantLimit)
			}
		})
	}
}

func TestPager_TakeTrimsToWhatIsLeft(t *testing.T) {
	p := newPager(Query{MaxResults: 150}, Limits{PageSize: 100})
	if n := p.take(100); n != 100 {
		t.Errorf("first take %d, want 100", n)
	}
	if n := p.take(100); n != 50 {
		t.Errorf("second take %d, want the 50 left", n)
	}
	if n := p.take(100); n != 0 {
		t.Errorf("third take %d, want 0", n)
	}
}

// TestPager_StopsRunawaySearches: without --max-requests (--all), an API that
// never says it is done cannot keep the search going forever.
func TestPager_StopsRunawaySearches(t *testing.T) {
	p := newPager(Query{}, Limits{PageSize: 100})
	pages := 0
	for pages < 5000 {
		pages++
		p.take(100)
		if !p.next(100, -1, true) {
			break
		}
	}
	if pages != MaxPagesWithoutLimit {
		t.Errorf("stopped after %d pages, want %d", pages, MaxPagesWithoutLimit)
	}
	if s := p.stats(); !s.Truncated || s.Limit != LimitSafety {
		t.Errorf("stats %+v, want truncated by the safety limit", s)
	}

	p = newPager(Query{}, Limits{PageSize: 100})
	pages = 0
	for pages < 100 {
		pages++
		if !p.next(0, -1, true) {
			break
		}
	}
	if pages != maxEmptyPages {
		t.Errorf("stopped after %d empty pages, want %d", pages, maxEmptyPages)
	}
	if s := p.stats(); !s.Truncated || s.Limit != LimitAPI {
		t.Errorf("stats %+v, want truncated: the API kept sending empty pages", s)
	}

	p = newPager(Query{}, Limits{PageSize: 100})
	if p.repeated("cursor-1") || p.repeated("cursor-2") {
		t.Fatal("new cursors are not repeats")
	}
	if !p.repeated("cursor-1") {
		t.Error("a cursor seen before is a repeat")
	}
	if s := p.stats(); !s.Truncated || s.Limit != LimitAPI {
		t.Errorf("stats %+v, want truncated by the repeat", s)
	}
}
