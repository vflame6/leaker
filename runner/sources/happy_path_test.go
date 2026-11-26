package sources

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vflame6/leaker/internal/fakeapi"
)

// happyCase runs one source against recorded API responses kept in
// testdata/<dir>/ and compares the results with want, in order.
type happyCase struct {
	name   string
	dir    string
	source func() Source
	st     ScanType
	target string
	// routes maps "METHOD host/path" to a fixture file in testdata/<dir>/.
	routes map[string]string
	want   []Result
	// check inspects the requests the source sent (optional).
	check func(t *testing.T, requests []fakeapi.Request)
	// runs repeats the case to catch nondeterministic ordering (default 1).
	runs int
}

func happyCases() []happyCase {
	const md5 = "5f4dcc3b5aa765d61d8327deb882cf99"
	return []happyCase{
		{
			name: "breachdirectory", dir: "breachdirectory",
			source: func() Source { return withKeys(&BreachDirectory{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET breachdirectory.p.rapidapi.com/": "search.json"},
			want: []Result{
				{Source: "breachdirectory", Email: "jane@example.com", Password: "hunter2", Hash: "f3bbbd66a63d4bf1747940578ec3d0103530e21d", Database: "Example Breach 2019"},
				{Source: "breachdirectory", Email: "jane@example.com", Hash: md5},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "X-Rapidapi-Key", testKey)
				expectQuery(t, reqs[0], "func", "auto")
			},
		},
		{
			name: "breachhouse", dir: "breachhouse",
			source: func() Source { return withKeys(&BreachHouse{}, testKey) },
			st:     TypeDomain, target: "Example.com",
			routes: map[string]string{"GET breach.house/api/v1/search": "search.json"},
			want: []Result{
				{Source: "breachhouse", Database: "Example Corp", URL: "example.com", Extra: map[string]string{
					"category": "ransomware", "group": "lockbit", "country": "US", "date": "2024-03-01", "record_id": "101"}},
				{Source: "breachhouse", Database: "Example.com forum dump", URL: "https://forum.example.com", Extra: map[string]string{
					"category": "breaches", "date": "2023-11-20", "record_id": "b-7"}},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Authorization", "Bearer "+testKey)
				expectQuery(t, reqs[0], "q", "example.com")
			},
		},
		{
			name: "dehashed", dir: "dehashed",
			source: func() Source { return withKeys(&DeHashed{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"POST api.dehashed.com/v2/search": "search.json"},
			want: []Result{{Source: "dehashed", Email: "jane@example.com", Username: "jane", Password: "hunter2",
				Name: "Jane Doe", Phone: "+1 555 234 1096", IP: "203.0.113.7", Database: "ExampleDB"}},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Dehashed-Api-Key", testKey)
				// No --max-results here: one page of the 10,000 a query returns.
				expectJSONBody(t, reqs[0], `{"query":"email:jane@example.com","page":1,"size":10000,"de_dupe":true}`)
			},
		},
		{
			name: "hibr", dir: "hibr",
			source: func() Source { return withKeys(&HIBR{}, testKey) },
			st:     TypeDomain, target: "example.com",
			routes: map[string]string{"GET haveibeenransom.com/api/metadata/domain/example.com": "domain.json"},
			want: []Result{
				{Source: "hibr", Database: "Example Corp", URL: "www.example.com", Extra: map[string]string{
					"category": "ransomware", "group": "akira", "country": "DE", "date": "2024-05-02", "record_id": "hibr-1"}},
				{Source: "hibr", Database: "Example 2022 breach", URL: "example.com", Extra: map[string]string{
					"category": "breaches", "date": "2022-01-15", "record_id": "hibr-2"}},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Authorization", "Bearer "+testKey)
			},
		},
		{
			name: "hudsonrock (free)", dir: "hudsonrock",
			source: func() Source { return &HudsonRock{} },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET cavalier.hudsonrock.com/api/json/v2/osint-tools/search-by-email": "free-email.json"},
			want: []Result{{Source: "hudsonrock", IP: "198.51.100.4", Extra: map[string]string{
				"computer_name": "DESKTOP-1", "operating_system": "Windows 10",
				"date_compromised": "2023-04-01T00:00:00.000Z", "stealer_family": "RedLine"}}},
		},
		{
			name: "hudsonrock (paid)", dir: "hudsonrock",
			source: func() Source { return withKeys(&HudsonRock{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET api.hudsonrock.com/json/v3/search-by-domain": "paid-email.json"},
			want: []Result{{Source: "hudsonrock", Email: "jane@example.com", Username: "jane", Password: "hunter2",
				IP: "198.51.100.4", URL: "https://login.example.com", Extra: map[string]string{"stealer_family": "Raccoon"}}},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Api-Key", testKey)
				expectQuery(t, reqs[0], "type", "email")
			},
		},
		{
			name: "intelx", dir: "intelx",
			source: func() Source {
				return withKeys(&IntelX{pollDelay: time.Millisecond}, "free.intelx.io:"+testKey)
			},
			st: TypeEmail, target: "jane@example.com",
			routes: map[string]string{
				"POST free.intelx.io/intelligent/search":          "search.json",
				"GET free.intelx.io/intelligent/search/result":    "result.json",
				"GET free.intelx.io/intelligent/search/terminate": "search.json",
				"GET free.intelx.io/file/read":                    "combo.txt",
			},
			want: []Result{
				{Source: "intelx", Email: "jane@example.com", Password: "hunter2"},
				{Source: "intelx", Email: "JANE@example.com", Password: "s3cret"},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				for _, r := range reqs {
					expectHeader(t, r, "X-Key", testKey)
					if r.URL.Path == "/file/read" && r.URL.Query().Get("storageid") != "st-1" {
						t.Errorf("read a file outside the leak buckets: %s", r.URL)
					}
				}
			},
		},
		{
			name: "leakcheck", dir: "leakcheck",
			source: func() Source { return withKeys(&LeakCheck{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET leakcheck.io/api/v2/query/jane@example.com": "email.json"},
			want: []Result{
				{Source: "leakcheck", Email: "jane@example.com", Password: "hunter2", Username: "jane", Name: "Jane Doe",
					Database: "Example.com", Extra: map[string]string{"dob": "1990-01-01"}},
				{Source: "leakcheck", Email: "jane@example.com"},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "X-Api-Key", testKey)
				expectQuery(t, reqs[0], "type", "email")
			},
		},
		{
			name: "leaklookup", dir: "leaklookup", runs: 20,
			source: func() Source { return withKeys(&LeakLookup{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"POST leak-lookup.com/api/search": "search.json"},
			want: []Result{
				{Source: "leaklookup", Database: "alpha.org", Email: "jane@example.com", Hash: md5, IP: "203.0.113.9", Name: "Jane Doe"},
				{Source: "leaklookup", Database: "mid.net"},
				{Source: "leaklookup", Database: "zeta.com", Email: "jane@example.com", Password: "hunter2", Username: "jane"},
			},
		},
		{
			name: "leakradar", dir: "leakradar",
			source: func() Source { return withKeys(&LeakRadar{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"POST api.leakradar.io/search/email": "email.json"},
			want: []Result{{Source: "leakradar", Email: "jane@example.com", Password: "hunter2", URL: "https://login.example.com",
				Extra: map[string]string{"leak_id": "lr-1", "password_strength": "2", "status": "new", "added_at": "2024-01-02", "category": "stealer"}}},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Authorization", "Bearer "+testKey)
				expectJSONBody(t, reqs[0], `{"email":"jane@example.com","is_email":true}`)
			},
		},
		{
			name: "leaksight (url)", dir: "leaksight",
			source: func() Source { return withKeys(&LeakSight{}, testKey) },
			st:     TypeDomain, target: "example.com",
			routes: map[string]string{"GET api.leaksight.com/osint/url": "url.json"},
			want: []Result{
				{Source: "leaksight", Username: "jane@example.com", Password: "hunter2", URL: "https://login.example.com/signin"},
				{Source: "leaksight", Username: "bob", Password: "pw", URL: "https://mail.example.com"},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectQuery(t, reqs[0], "token", testKey)
			},
		},
		{
			name: "leaksight (username)", dir: "leaksight",
			source: func() Source { return withKeys(&LeakSight{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET api.leaksight.com/osint/username": "username.json"},
			want: []Result{
				{Source: "leaksight", Email: "jane@example.com", Password: "hunter2", URL: "https://shop.example.com",
					Extra: map[string]string{"leak_type": "stealer_json"}},
				{Source: "leaksight", Extra: map[string]string{"database_url": "https://dumps.example.net/example.sql"}},
			},
		},
		{
			name: "osintleak", dir: "osintleak",
			source: func() Source { return withKeys(&OSINTLeak{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET osintleak.com/api/v1/search_api/": "email.json"},
			want: []Result{{Source: "osintleak", Email: "jane@example.com", Password: "hunter2", Name: "Jane Doe",
				IP: "203.0.113.5", URL: "https://example.com/login", Database: "Example Logs 2023",
				Extra: map[string]string{"city": "Springfield"}}},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectQuery(t, reqs[0], "api_key", testKey)
				expectQuery(t, reqs[0], "type", "email")
			},
		},
		{
			name: "proxynova", dir: "proxynova",
			source: func() Source { return &ProxyNova{} },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"GET api.proxynova.com/comb": "comb.json"},
			want: []Result{
				{Source: "proxynova", Email: "jane@example.com", Password: "hunter2"},
				{Source: "proxynova", Email: "jane@example.com.au", Password: "other"},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectQuery(t, reqs[0], "limit", "100")
			},
		},
		{
			name: "snusbase", dir: "snusbase", runs: 20,
			source: func() Source { return withKeys(&Snusbase{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{
				"POST api.snusbase.com/data/search":        "search.json",
				"POST api.snusbase.com/tools/combo-lookup": "combo.json",
				"POST api.snusbase.com/tools/hash-lookup":  "hash.json",
				"POST api.snusbase.com/tools/ip-whois":     "whois.json",
			},
			want: []Result{
				{Source: "snusbase", Database: "ALPHA_ORG", Email: "jane@example.com", Password: "hunter2", Name: "Jane Doe", Salt: "x1"},
				{Source: "snusbase", Database: "ZETA_COM", Email: "jane@example.com", Username: "jane", Hash: md5,
					Password: "password", IP: "203.0.113.8", Extra: map[string]string{"ip_geo": "Springfield, US (ExampleNet)"}},
				{Source: "snusbase", Database: "COMBO_LIST", Username: "jane@example.com", Password: "combo-pass"},
			},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Auth", testKey)
			},
		},
		{
			name: "weleakinfo", dir: "weleakinfo",
			source: func() Source { return withKeys(&WeLeakInfo{}, "pub:"+testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"POST api.weleakinfo.io/v3/search": "search.json"},
			want: []Result{{Source: "weleakinfo", Email: "jane@example.com", Username: "jane", Password: "hunter2",
				Name: "Jane", IP: "203.0.113.3", Phone: "15552341096"}},
			check: func(t *testing.T, reqs []fakeapi.Request) {
				expectHeader(t, reqs[0], "Authorization", "Bearer "+testKey)
				expectJSONBody(t, reqs[0], `{"query":"jane@example.com","type":"email","limit":"1000","wildcard":"false"}`)
			},
		},
		{
			name: "whiteintel", dir: "whiteintel",
			source: func() Source { return withKeys(&WhiteIntel{}, testKey) },
			st:     TypeEmail, target: "jane@example.com",
			routes: map[string]string{"POST api.whiteintel.io/get_consumer_leaks.php": "search.json"},
			want: []Result{{Source: "whiteintel", Username: "jane@example.com", Password: "hunter2", IP: "198.51.100.9",
				URL: "https://login.example.com", Extra: map[string]string{
					"data_type": "consumer", "hostname": "DESKTOP-9", "log_date": "2024-02-03",
					"malware_path": `C:\Users\jane\AppData\stealer.exe`}}},
		},
	}
}

func TestSources_HappyPath(t *testing.T) {
	for _, hc := range happyCases() {
		t.Run(hc.name, func(t *testing.T) {
			runs := hc.runs
			if runs == 0 {
				runs = 1
			}
			for run := 0; run < runs; run++ {
				srv := fakeapi.NewServer(t, fixtureHandler(t, hc.dir, hc.routes))
				results, errs := runSource(t, hc.source(), hc.target, hc.st, srv.Transport())
				for _, err := range errs {
					t.Fatalf("run %d: unexpected error: %v", run, err)
				}
				if !reflect.DeepEqual(results, hc.want) {
					t.Fatalf("run %d: results differ\n got: %+v\nwant: %+v", run, results, hc.want)
				}
				if hc.check != nil && run == 0 {
					hc.check(t, srv.Requests())
				}
			}
		})
	}
}

// fixtureHandler serves testdata/<dir>/<file> for each route and fails the
// test on any other request.
func fixtureHandler(t *testing.T, dir string, routes map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.Host + r.URL.Path
		file, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", dir, file))
		if err != nil {
			t.Errorf("fixture: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	})
}

func expectHeader(t *testing.T, r fakeapi.Request, name, want string) {
	t.Helper()
	if got := r.Header.Get(name); got != want {
		t.Errorf("%s %s: header %s = %q, want %q", r.Method, r.URL.Path, name, got, want)
	}
}

func expectQuery(t *testing.T, r fakeapi.Request, name, want string) {
	t.Helper()
	if got := r.URL.Query().Get(name); got != want {
		t.Errorf("%s %s: query %s = %q, want %q", r.Method, r.URL.Path, name, got, want)
	}
}

func expectJSONBody(t *testing.T, r fakeapi.Request, want string) {
	t.Helper()
	if string(r.Body) != want {
		t.Errorf("%s %s: body %s, want %s", r.Method, r.URL.Path, r.Body, want)
	}
}

// TestDeHashed_RateLimitedTwiceYieldsOneResultSet is the B1 acceptance check:
// two 429 answers and then the data give one result set from three requests.
func TestDeHashed_RateLimitedTwiceYieldsOneResultSet(t *testing.T) {
	hc := happyCaseNamed(t, "dehashed")
	fixtures := fixtureHandler(t, hc.dir, hc.routes)
	var calls atomic.Int32
	srv := fakeapi.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fixtures.ServeHTTP(w, r)
	}))
	results, errs := runSource(t, hc.source(), hc.target, hc.st, srv.Transport())
	for _, err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(results, hc.want) {
		t.Errorf("results differ\n got: %+v\nwant: %+v", results, hc.want)
	}
	if n := len(srv.Requests()); n != 3 {
		t.Errorf("server saw %d requests, want 3", n)
	}
}

func happyCaseNamed(t *testing.T, name string) happyCase {
	t.Helper()
	for _, hc := range happyCases() {
		if hc.name == name {
			return hc
		}
	}
	t.Fatalf("no happy case %q", name)
	return happyCase{}
}
