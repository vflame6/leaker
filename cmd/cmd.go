package cmd

import (
	"bufio"
	"context"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner"
	"github.com/vflame6/leaker/runner/sources"
	"io"
	"os"
	"strings"
	"time"
)

var CLI struct {
	// COMMAND
	Domain struct {
		Targets string `arg:"" optional:"" complete:"file" help:"Target domain or file with domains, one per line"`
	} `cmd:"" help:"Search by domain name."`
	Email struct {
		Targets string `arg:"" optional:"" complete:"file" help:"Target email or file with emails, one per line"`
	} `cmd:"" help:"Search by email address."`
	Keyword struct {
		Targets string `arg:"" optional:"" complete:"file" help:"Target keyword or file with keywords, one per line"`
	} `cmd:"" help:"Search by keyword."`
	Phone struct {
		Targets string `arg:"" optional:"" complete:"file" help:"Target phone number or file with phone numbers, one per line"`
	} `cmd:"" help:"Search by phone number."`
	Username struct {
		Targets string `arg:"" optional:"" complete:"file" help:"Target username or file with usernames, one per line"`
	} `cmd:"" help:"Search by username."`

	// INFORMATION
	SourcesCmd struct{} `cmd:"" name:"sources" help:"List the sources with their key state, search types and limits."`

	// SHELL INTEGRATION
	Completion struct {
		Shell string `arg:"" enum:"bash,zsh,fish" help:"Shell to print the script for: bash, zsh or fish."`
	} `cmd:"" help:"Print the shell completion script."`
	Man struct{} `cmd:"" hidden:"" help:"Print the man page."`

	// INPUT
	List    string   `short:"l" placeholder:"FILE" complete:"file" help:"File with targets, one per line (- reads stdin)"`
	Sources []string `short:"s" default:"online" complete:"sources" help:"Sources to use for enumeration. online (default), all, local, or explicit source names."`

	// OPTIMIZATION
	Timeout     time.Duration `help:"Seconds to wait on each request before timing out" default:"30s"`
	NoRateLimit bool          `short:"N" help:"Disable rate limiting (DANGER)"`
	Threads     int           `short:"t" default:"10" help:"Targets to search at the same time"`
	MaxResults  int           `default:"1000" help:"Records to fetch per source and target at most"`
	MaxRequests int           `default:"10" help:"Search requests per source and target at most, to spare API credits"`
	All         bool          `help:"Fetch every record up to each API's cap (lifts --max-results, and --max-requests unless given)"`

	// OUTPUT
	JSON            bool   `short:"j" help:"Output results as JSONL (one JSON object per line)"`
	NoDeduplication bool   `help:"Disable deduplication of results across sources"`
	NoFilter        bool   `help:"Disable results filtering, include every result"`
	Output          string `short:"o" placeholder:"FILE" complete:"file" help:"File to write output to"`
	Overwrite       bool   `help:"Force overwrite of existing output file"`
	Stats           bool   `help:"Print a table of every source's status after each target"`
	StatsFile       string `placeholder:"FILE" complete:"file" help:"Write the per-source table of every target to FILE as JSON"`

	// CONFIGURATION
	ProviderConfig string `short:"p" placeholder:"FILE" complete:"file" help:"Provider config file"`
	Proxy          string `help:"Proxy URL for every request (default: HTTP_PROXY/HTTPS_PROXY from the environment)"`
	UserAgent      string `short:"A" help:"Custom user agent"`
	Insecure       bool   `help:"Disable TLS certificate verification (use with caution)"`
	DB             string `placeholder:"FILE" complete:"file" help:"Path to the local SQLite cache DB"`
	NoWriteDB      bool   `help:"Disable writing results to the local SQLite cache"`

	// DEBUG
	Version       bool `help:"Print version of leaker"`
	Quiet         bool `short:"q" help:"Suppress output, print results only"`
	Verbose       bool `short:"v" help:"Show sources in results output"`
	Metadata      bool `short:"M" help:"Include metadata fields (database) in plain output; JSON always includes them"`
	Debug         bool `short:"D" help:"Enable debug mode"`
	NoColor       bool `help:"Disable colored output"`
	ClearDatabase bool `short:"C" help:"Clear all cached rows from the local SQLite database"`
}

func resolveDBPath(flagValue string, getenv func(string) string) string {
	if flagValue != "" {
		return flagValue
	}
	return getenv("LEAKER_DB")
}

func confirmClearDatabase(input io.Reader, output io.Writer, path string) bool {
	_, _ = fmt.Fprintf(output, "Clear all rows in the local database at %s? [y/N] ", path)
	answer, _ := bufio.NewReader(input).ReadString('\n')
	_, _ = fmt.Fprintln(output)
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}

func resolveNoWriteDB(flagValue bool, getenv func(string) string, warnf func(string, ...any)) bool {
	if flagValue {
		return true
	}
	envValue := getenv("LEAKER_NO_WRITE_DB")
	switch strings.ToLower(strings.TrimSpace(envValue)) {
	case "true", "1":
		return true
	case "", "false", "0":
		return false
	default:
		warnf("ignoring unparseable LEAKER_NO_WRITE_DB value: %q", envValue)
		return false
	}
}

// newParser builds the command-line parser for CLI. The completion scripts
// and the man page are generated from its model.
func newParser() (*kong.Kong, error) {
	return kong.New(&CLI,
		kong.Name("leaker"),
		kong.Description("leaker is a leak discovery tool that returns valid credential leaks for emails, using passive online sources."),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
		kong.Exit(func(code int) { os.Exit(usageExitCode(code)) }))
}

func Run() {
	parser, err := newParser()
	if err != nil {
		panic(err)
	}

	ctx, parseErr := parser.Parse(os.Args[1:])

	// These flags don't require a command; handle them before enforcing one.
	// show version
	if CLI.Version {
		fmt.Println(VERSION)
		os.Exit(0)
	}
	// clear the local database without requiring a scan command
	if CLI.ClearDatabase {
		dbPath := (&runner.Options{DBPath: resolveDBPath(CLI.DB, os.Getenv)}).ResolvedDBPath()
		if !confirmClearDatabase(os.Stdin, os.Stderr, dbPath) {
			fmt.Fprintln(os.Stderr, "Cancelled; local database was not changed.")
			os.Exit(0)
		}
		rows, exists, err := runner.ClearDatabase(dbPath)
		if err != nil {
			logger.Fatal(err)
		}
		if !exists {
			fmt.Fprintf(os.Stderr, "No local database found at %s; nothing to clear.\n", dbPath)
		} else {
			fmt.Fprintf(os.Stderr, "Cleared %d row(s) from the local database at %s.\n", rows, dbPath)
		}
		os.Exit(0)
	}

	// error if no command is specified
	if parseErr != nil {
		if len(os.Args) == 1 {
			if pe, ok := parseErr.(*kong.ParseError); ok {
				_ = pe.Context.PrintUsage(false)
			}
			os.Exit(0)
		}
		parser.FatalIfErrorf(parseErr)
	}

	// Shell integration prints to stdout before the banner and never touches
	// the config or the database.
	switch ctx.Command() {
	case "completion <shell>":
		if err := writeCompletion(os.Stdout, parser.Model, CLI.Completion.Shell); err != nil {
			logger.Fatal(err)
		}
		os.Exit(ExitOK)
	case "man":
		if err := writeManPage(os.Stdout, parser.Model, VERSION, time.Now()); err != nil {
			logger.Fatal(err)
		}
		os.Exit(ExitOK)
	case "sources":
		os.Exit(listSources())
	}

	searchLimit, err := searchLimits(CLI.MaxResults, CLI.MaxRequests, CLI.All, flagTyped(ctx, "max-requests"))
	if err == nil && CLI.Threads < 1 {
		err = fmt.Errorf("--threads must be at least 1")
	}
	if err != nil {
		parser.Errorf("%s", err)
		os.Exit(ExitUsage)
	}

	// output banner
	if !CLI.Quiet {
		PrintBanner()
	}

	// select command
	var scanType sources.ScanType
	var targets string

	switch ctx.Command() {
	case "email", "email <targets>":
		scanType = sources.TypeEmail
		targets = CLI.Email.Targets
	case "username", "username <targets>":
		scanType = sources.TypeUsername
		targets = CLI.Username.Targets
	case "domain", "domain <targets>":
		scanType = sources.TypeDomain
		targets = CLI.Domain.Targets
	case "keyword", "keyword <targets>":
		scanType = sources.TypeKeyword
		targets = CLI.Keyword.Targets
	case "phone", "phone <targets>":
		scanType = sources.TypePhone
		targets = CLI.Phone.Targets
	default:
		logger.Fatalf("Unknown command: %s", ctx.Command())
	}

	// Resolve --db / LEAKER_DB. Flag wins; fall back to env; empty means
	// "use the runner default location".
	dbPath := resolveDBPath(CLI.DB, os.Getenv)

	// Resolve --no-write-db / LEAKER_NO_WRITE_DB. Flag wins when set true;
	// otherwise parse the env value. Unparseable env values log a warning
	// and are treated as false.
	noWriteDB := resolveNoWriteDB(CLI.NoWriteDB, os.Getenv, logger.Warnf)

	options := &runner.Options{
		Debug:            CLI.Debug,
		Insecure:         CLI.Insecure,
		JSON:             CLI.JSON,
		ListFile:         CLI.List,
		MaxRequests:      searchLimit.maxRequests,
		MaxRequestsTyped: searchLimit.maxRequestsTyped,
		MaxResults:       searchLimit.maxResults,
		NoColor:          CLI.NoColor,
		NoDeduplication:  CLI.NoDeduplication,
		NoFilter:         CLI.NoFilter,
		NoRateLimit:      CLI.NoRateLimit,
		OutputFile:       CLI.Output,
		Overwrite:        CLI.Overwrite,
		ProviderConfig:   CLI.ProviderConfig,
		Proxy:            CLI.Proxy,
		Quiet:            CLI.Quiet,
		Sources:          CLI.Sources,
		StatsFile:        CLI.StatsFile,
		Summary:          CLI.Stats,
		Targets:          targets,
		Threads:          CLI.Threads,
		Timeout:          CLI.Timeout,
		Type:             scanType,
		UserAgent:        CLI.UserAgent,
		Verbose:          CLI.Verbose,
		Metadata:         CLI.Metadata,
		Version:          VERSION,
		DBPath:           dbPath,
		NoWriteDB:        noWriteDB,
	}

	runCtx, stop := signalContext()
	code := run(runCtx, options)
	stop()
	os.Exit(code)
}

// limits are the --max-results and --max-requests of a search, 0 meaning no
// limit, and whether --max-requests was given (sources with their own
// default budget use it otherwise).
type limits struct {
	maxResults, maxRequests int
	maxRequestsTyped        bool
}

// searchLimits returns the limits of a search. --all lifts both, but keeps
// a --max-requests the user typed.
func searchLimits(maxResults, maxRequests int, all, maxRequestsTyped bool) (limits, error) {
	if maxResults < 1 {
		return limits{}, fmt.Errorf("--max-results must be at least 1 (use --all for no limit)")
	}
	if maxRequests < 1 {
		return limits{}, fmt.Errorf("--max-requests must be at least 1 (use --all for no limit)")
	}
	switch {
	case !all:
		return limits{maxResults, maxRequests, maxRequestsTyped}, nil
	case maxRequestsTyped:
		return limits{0, maxRequests, true}, nil
	}
	return limits{}, nil
}

// flagTyped reports whether the flag was given on the command line, as
// opposed to taking its default.
func flagTyped(ctx *kong.Context, name string) bool {
	for _, p := range ctx.Path {
		if p.Flag != nil && p.Flag.Name == name {
			return true
		}
	}
	return false
}

// listSources prints the source list ("leaker sources") and returns the
// exit code.
func listSources() int {
	err := runner.ListSources(&runner.Options{
		NoColor:        CLI.NoColor,
		Output:         os.Stdout,
		ProviderConfig: CLI.ProviderConfig,
		Quiet:          CLI.Quiet,
	})
	if err != nil {
		logger.Failf("%s", err)
	}
	return exitCode(err)
}

// run performs the scan and returns the process exit code.
func run(ctx context.Context, options *runner.Options) int {
	r, err := runner.NewRunner(options)
	if err == nil {
		err = r.RunEnumeration(ctx)
		if closeErr := r.Close(); closeErr != nil {
			logger.Errorf("could not close the local database: %s", closeErr)
		}
	}
	code := exitCode(err)
	if err != nil && code != ExitInterrupted {
		logger.Failf("%s", err)
	}
	return code
}
