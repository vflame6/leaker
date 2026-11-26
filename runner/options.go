package runner

import (
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"github.com/vflame6/leaker/utils"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

var (
	configDir                     = utils.AppConfigDirOrDefault(".", "leaker")
	defaultProviderConfigLocation = utils.GetEnvOrDefault("LEAKER_PROVIDER_CONFIG", filepath.Join(configDir, "provider-config.yaml"))
	defaultDBLocation             = filepath.Join(configDir, "leaker.db")
)

// Options struct is used to store leaker options. Sort alphabetically
type Options struct {
	DBPath      string // DBPath is the local SQLite cache path (empty = use default)
	Debug       bool
	Metadata    bool   // Metadata includes metadata fields (database) in output
	Insecure    bool   // Insecure disables TLS certificate verification when true
	JSON        bool   // JSON outputs results as JSONL (one JSON object per line)
	ListFile    string // ListFile is the -l/--list file with targets, one per line ("-" reads stdin)
	MaxRequests int    // MaxRequests caps the search requests per source and target (0 = no limit)
	// MaxRequestsTyped is true when --max-requests was given; otherwise a
	// source may use its own default (Limits.DefaultRequests).
	MaxRequestsTyped bool
	MaxResults       int  // MaxResults caps the records per source and target (0 = as many as the API returns)
	NoColor          bool // NoColor disables colored output
	NoDeduplication  bool // NoDeduplication disables deduplication of results across sources
	NoFilter         bool
	NoRateLimit      bool
	NoWriteDB        bool // NoWriteDB disables writing to the local SQLite cache
	Output           io.Writer
	OutputFile       string
	Overwrite        bool
	ProviderConfig   string // ProviderConfig contains the location of the provider config file
	Proxy            string
	Quiet            bool
	Sources          []string
	StatsFile        string    // StatsFile is the --stats-file path (empty = none)
	Summary          bool      // Summary prints each target's per-source table (--stats)
	Stdin            bool      // Stdin is true when stdin is piped or redirected, not a terminal
	StdinReader      io.Reader // StdinReader replaces os.Stdin (tests only)
	SummaryOutput    io.Writer // SummaryOutput receives the per-target tables (default stderr)
	Targets          string    // Targets is the positional target ("-" reads stdin)
	Threads          int       // Threads caps the targets in flight (0 = default of 10)
	Timeout          time.Duration
	Transport        http.RoundTripper // Transport replaces the network transport (tests only)
	Type             sources.ScanType
	UserAgent        string
	Verbose          bool
	Version          string
}

// ResolvedDBPath returns the effective local DB path: the user-provided
// DBPath if non-empty, otherwise the default under the user config dir.
func (options *Options) ResolvedDBPath() string {
	if options.DBPath != "" {
		return options.DBPath
	}
	return defaultDBLocation
}

// ListSources prints every source with its key state, the types it can
// search and its rate limit ("leaker sources"). Keys are read from the
// provider config when it exists; it is not created.
func ListSources(options *Options) error {
	if options == nil {
		options = &Options{}
	}
	if options.ProviderConfig == "" {
		options.ProviderConfig = defaultProviderConfigLocation
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	options.ConfigureOutput()

	list := NewSources()
	if utils.FileExists(options.ProviderConfig) {
		if err := loadProviderKeys(options.ProviderConfig, list); err != nil {
			return configErrorf("could not read provider config %s: %w", options.ProviderConfig, err)
		}
		logger.Infof("Keys are read from %s", options.ProviderConfig)
	} else {
		logger.Infof("Keys will be read from %s; the first search creates it", options.ProviderConfig)
	}
	slices.SortFunc(list, func(a, b sources.Source) int {
		return strings.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
	})

	w := options.Output
	_, _ = fmt.Fprintln(w, "Source groups:")
	_, _ = fmt.Fprintln(w, "  online   all online API sources (default)")
	_, _ = fmt.Fprintln(w, "  all      every source including local")
	_, _ = fmt.Fprintln(w, "  local    local SQLite database")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Available sources:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SOURCE\tKEY\tTYPES\tINTERVAL\tPAGE\tMAX")
	for _, s := range list {
		limits := s.Limits()
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name(), keyState(s), typeNames(s.Types(), " "),
			intervalCell(s.MinInterval()), countCell(limits.PageSize), countCell(limits.MaxResults))
	}
	return tw.Flush()
}

// countCell shows a limit, or "-" when there is none.
func countCell(n int) string {
	if n <= 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// keyState says whether a source has the key it needs.
func keyState(s sources.Source) string {
	switch {
	case s.Name() == sources.LocalSourceName:
		return "-"
	case !s.UsesKey():
		return "not needed"
	case s.HasKeys():
		return "configured"
	case s.NeedsKey():
		return "missing"
	}
	return "optional"
}

// intervalCell is the minimum time between two requests of a source.
func intervalCell(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Round(time.Millisecond).String()
}

// ConfigureOutput configures the output on the screen
func (options *Options) ConfigureOutput() {
	logger.DefaultLogger.SetMaxLevel(logger.LevelInfo)
	logger.SetNoColor(false)

	// If the user desires verbose output, show verbose output
	if options.Debug {
		logger.DefaultLogger.SetMaxLevel(logger.LevelVerbose)
	}
	if options.Quiet {
		logger.DefaultLogger.SetMaxLevel(logger.LevelFatal)
	}
	if options.NoColor {
		logger.SetNoColor(true)
	}
}
