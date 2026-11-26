package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"github.com/vflame6/leaker/utils"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
)

type Runner struct {
	options *Options
	// available holds this run's own source instances, with keys loaded.
	available []sources.Source
	// scanSources are the selected sources that can run.
	scanSources []sources.Source
	// providerConfig is the provider config file in use.
	providerConfig string
	// leakerDB is the local SQLite cache handle. May be nil when writes
	// are disabled and the DB does not exist on disk.
	leakerDB *LeakerDB
	// dbNotice tells the user, once per run, where results are stored.
	dbNotice sync.Once
	// session is the HTTP session shared by every source and the verifier
	// for the whole run.
	session *sources.Session
	// verifier enriches results when -V is given; one per run.
	verifier *Verifier
}

// Close releases resources held by the runner (currently just the local
// DB handle, if any). Safe on a nil receiver.
func (r *Runner) Close() error {
	if r == nil {
		return nil
	}
	if r.session != nil {
		r.session.Close()
	}
	return r.leakerDB.Close()
}

// NewRunner creates a new runner struct instance by parsing
// the configuration options, configuring sources, reading lists
// and setting up loggers, etc. Usage and configuration problems are
// returned as *ConfigError before anything is sent.
func NewRunner(options *Options) (*Runner, error) {
	options.ConfigureOutput()

	r := &Runner{
		options:   options,
		available: NewSources(),
	}

	configPath, err := resolveProviderConfig(options.ProviderConfig)
	if err != nil {
		return nil, err
	}
	r.providerConfig = configPath
	restrictPermissions(configPath, "Provider config")
	logger.Infof("Loading provider config from %s", configPath)
	if err := loadProviderKeys(configPath, r.available); err != nil {
		return nil, configErrorf("could not read provider config %s: %w", configPath, err)
	}

	if err := r.configureSources(); err != nil {
		return nil, err
	}

	// Default output is stdout
	if options.Output == nil {
		options.Output = os.Stdout
	}

	// Check if stdin pipe was given
	options.Stdin = utils.HasStdin()

	// configure User Agent
	// if not specified, will set the default User-Agent string
	if options.UserAgent == "" {
		options.UserAgent = fmt.Sprintf("leaker/%s", options.Version)
	}

	// One session for the whole run: every source and the verifier share
	// its proxy and TLS settings. Without --proxy, HTTP(S)_PROXY apply.
	session, err := sources.NewSession(sources.SessionOptions{
		Timeout:   options.Timeout,
		UserAgent: options.UserAgent,
		Proxy:     options.Proxy,
		Insecure:  options.Insecure,
		Transport: options.Transport,
	})
	if err != nil {
		return nil, &ConfigError{Err: err}
	}
	r.session = session
	r.verifier = NewVerifier(options.Verify, session)

	// Open the local DB cache. In writable mode, the file is created if
	// missing; in read-only mode, a missing file yields a nil handle
	// (a warning is logged below). A corrupt / incompatible schema is
	// an error.
	dbPath := options.ResolvedDBPath()
	writable := !options.NoWriteDB
	if writable {
		logger.Debugf("Using DB in read-write mode from: %s", dbPath)
	} else {
		logger.Debugf("Using database in read-only mode from: %s", dbPath)
	}
	db, err := OpenLeakerDB(dbPath, writable)
	if err != nil {
		return nil, fmt.Errorf("cannot open local DB at %s: %w", dbPath, err)
	}
	if db == nil {
		// Read-only mode, no DB on disk.
		logger.Warnf("local DB at %s does not exist; local search will return no results", dbPath)
	}
	if db != nil {
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			restrictPermissions(p, "Local database file")
		}
	}
	r.leakerDB = db

	// Inject the lookup function into this run's LocalDB source so its
	// Run() can call LeakerDB.Search without importing the runner
	// package (which would create a cycle).
	for _, s := range r.available {
		if ldb, ok := s.(*sources.LocalDB); ok && db != nil {
			ldb.Lookup = db.Search
		}
	}

	return r, nil
}

// restrictPermissions makes a file holding secrets private to the user,
// with a warning, when other users could access it.
func restrictPermissions(path, what string) {
	previous, changed, err := utils.RestrictToOwner(path)
	if err != nil {
		logger.Warnf("%s %s may be readable by other users: %s", what, path, err)
		return
	}
	if changed {
		logger.Warnf("%s %s was accessible to other users (mode %04o); changed to %04o", what, path, previous, previous&^0o077)
	}
}

// resolveProviderConfig returns the provider config file to load. A file
// given with -p must exist; the default one is created on first use.
func resolveProviderConfig(explicit string) (string, error) {
	if explicit != "" {
		if !utils.FileExists(explicit) {
			return "", configErrorf("provider config %s does not exist", explicit)
		}
		return explicit, nil
	}
	if !utils.FileExists(defaultProviderConfigLocation) {
		logger.Debugf("Creating new default provider config at %s", defaultProviderConfigLocation)
		if err := createProviderConfigYAML(defaultProviderConfigLocation); err != nil {
			return "", fmt.Errorf("could not create provider config %s: %w", defaultProviderConfigLocation, err)
		}
	}
	return defaultProviderConfigLocation, nil
}

// configureSources resolves -s into the sources to run. A source named
// explicitly without its API key is a config error, so a search that cannot
// run never reports "0 leaks". The groups "online" and "all" skip such
// sources and say how many were skipped.
func (r *Runner) configureSources() error {
	known := map[string]bool{}
	for _, source := range r.available {
		known[source.Name()] = true
	}

	explicit := map[string]bool{}
	var hasAll, hasOnline bool
	for i := range r.options.Sources {
		name := strings.TrimSpace(strings.ToLower(r.options.Sources[i]))
		r.options.Sources[i] = name
		switch {
		case name == "all":
			hasAll = true
		case name == "online":
			hasOnline = true
		case known[name]:
			explicit[name] = true
		default:
			return configErrorf("invalid source \"%s\" specified in -s flag", name)
		}
	}
	if hasAll && hasOnline {
		logger.Debug("Source token 'online' is redundant when 'all' is specified")
	}

	var selected []sources.Source
	for _, source := range r.available {
		name := source.Name()
		if hasAll || explicit[name] || (hasOnline && name != sources.LocalSourceName) {
			selected = append(selected, source)
		}
	}

	scanType := r.options.Type
	var runnable, noKey, unsupported []sources.Source
	var missing, wrongType []string
	for _, source := range selected {
		switch {
		case source.NeedsKey() && !source.HasKeys():
			noKey = append(noKey, source)
			if explicit[source.Name()] {
				missing = append(missing, source.Name())
			}
		case !sources.SupportsScanType(source, scanType):
			unsupported = append(unsupported, source)
			if explicit[source.Name()] {
				wrongType = append(wrongType, source.Name())
			}
		default:
			runnable = append(runnable, source)
		}
	}

	if len(missing) > 0 {
		return configErrorf("no API key configured for %s: add keys to %s or remove %s from -s",
			strings.Join(missing, ", "), r.providerConfig, plural(len(missing), "it", "them"))
	}
	if len(wrongType) > 0 {
		return configErrorf("%s cannot run %s searches (domain searches only): remove %s from -s",
			strings.Join(wrongType, ", "), scanType, plural(len(wrongType), "it", "them"))
	}
	if len(runnable) == 0 {
		return configErrorf("no selected source can run a %s search; add keys to %s", scanType, r.providerConfig)
	}
	if skipped := len(noKey) + len(unsupported); skipped > 0 {
		var reasons []string
		if len(noKey) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d skipped (no key)", len(noKey)))
			logger.Debugf("Skipped sources without a key: %s", sourceNames(noKey))
		}
		if len(unsupported) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d skipped (no %s search)", len(unsupported), scanType))
			logger.Debugf("Skipped sources without %s search: %s", scanType, sourceNames(unsupported))
		}
		logger.Warnf("%d of %d sources active; %s", len(runnable), len(selected), strings.Join(reasons, "; "))
	}

	active := make([]string, 0, len(runnable))
	for _, source := range runnable {
		active = append(active, source.Name())
	}
	logger.Debugf("Configuring leaker to use sources: %s", strings.Join(active, ", "))
	r.scanSources = runnable
	return nil
}

func sourceNames(list []sources.Source) string {
	names := make([]string, 0, len(list))
	for _, source := range list {
		names = append(names, source.Name())
	}
	return strings.Join(names, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// RunEnumeration searches every target. It returns the context's error when
// the run is interrupted, and a *ConfigError for usage problems.
func (r *Runner) RunEnumeration(ctx context.Context) error {
	var err error

	// parse targets
	targets, closeTargets, err := r.openTargets()
	if err != nil {
		return err
	}
	defer closeTargets()

	// configure output
	outputs := []io.Writer{r.options.Output}

	if r.options.OutputFile != "" {
		// An existing output file is never clobbered unless --overwrite is
		// given; then it is truncated, so it holds this run's results only.
		file, err := utils.CreateOutputFile(r.options.OutputFile, r.options.Overwrite)
		if errors.Is(err, fs.ErrExist) {
			return configErrorf("output file %s already exists; use --overwrite to replace it", r.options.OutputFile)
		}
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				logger.Errorf("could not close output file %s: %s", r.options.OutputFile, err)
			}
		}()
		outputs = append(outputs, file)
	}

	return r.EnumerateMultipleTargets(ctx, targets, outputs)
}

// Target-syntax validators. The TLD quantifier is {2,} (not a fixed upper
// bound) so long TLDs like ".group" or ".photography" are accepted. Input is
// lower-cased before matching, so the email pattern only needs [a-z].
var (
	emailRegex  = regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}$`)
	domainRegex = regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)
	phoneRegex  = regexp.MustCompile(`^\d{10,15}$`)
)

// matchesTargetType reports whether line is a syntactically valid target for
// the given scan type. Phone input is expected to be digit-normalized already.
// Types without a syntax constraint (username, keyword) always pass.
func matchesTargetType(scanType sources.ScanType, line string) bool {
	switch scanType {
	case sources.TypeEmail:
		return emailRegex.MatchString(line)
	case sources.TypeDomain:
		return domainRegex.MatchString(line)
	case sources.TypePhone:
		return phoneRegex.MatchString(line)
	default:
		return true
	}
}

// EnumerateMultipleTargets searches each line of reader as a target. Lines
// are read in the background so that an interrupt is noticed at once, even
// while waiting for input. On interrupt it logs "Interrupted" once, starts no
// further target and returns the context's error.
func (r *Runner) EnumerateMultipleTargets(ctx context.Context, reader io.Reader, writers []io.Writer) error {
	if !r.options.NoFilter {
		logger.Debugf("Results filtering is enabled, leaker will filter results by matching every result to inputted target.")
	} else {
		logger.Debugf("Results filtering is disabled, leaker will not filter any result.")
	}

	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				readErr <- nil
				return
			}
		}
		readErr <- scanner.Err()
	}()

	interrupted := func() error {
		logger.Info("Interrupted")
		return ctx.Err()
	}

	var errs []error
	searched, failed := 0, 0
	for {
		var line string
		var ok bool
		select {
		case <-ctx.Done():
			return interrupted()
		case line, ok = <-lines:
		}
		if !ok {
			// The reader also stops when the run is cancelled.
			if ctx.Err() != nil {
				return interrupted()
			}
			if err := <-readErr; err != nil {
				errs = append(errs, fmt.Errorf("could not read targets: %w", err))
			}
			if searched == 0 && len(errs) == 0 {
				return configErrorf("no valid %s target in the input", r.options.Type)
			}
			if failed > 0 {
				errs = append(errs, fmt.Errorf("%d of %d targets could not be searched: every source failed", failed, searched))
			}
			return errors.Join(errs...)
		}
		if ctx.Err() != nil {
			return interrupted()
		}

		if strings.TrimSpace(line) == "" {
			continue
		}
		target := normalizeTarget(r.options.Type, line)
		// check if the line is a syntactically valid target for the scan type
		if !matchesTargetType(r.options.Type, target) {
			logger.Infof("Can't parse input as target, skipping: %s", strings.TrimSpace(line))
			continue
		}

		// run enumeration for a single line
		searched++
		if err := r.EnumerateSingleTarget(ctx, target, r.options.Type, writers); err != nil {
			if ctx.Err() != nil {
				return interrupted()
			}
			if errors.Is(err, errAllSourcesFailed) {
				failed++ // already reported by EnumerateSingleTarget
				continue
			}
			logger.Errorf("error enumerating %s: %s", target, err)
			errs = append(errs, err)
		}
	}
}
