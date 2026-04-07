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
	"os"
	"regexp"
	"slices"
	"strings"
)

type Runner struct {
	options     *Options
	scanSources []sources.Source
	// leakerDB is the local SQLite cache handle. May be nil when writes
	// are disabled and the DB does not exist on disk.
	leakerDB *LeakerDB
}

// Close releases resources held by the runner (currently just the local
// DB handle, if any). Safe on a nil receiver.
func (r *Runner) Close() error {
	if r == nil {
		return nil
	}
	return r.leakerDB.Close()
}

// NewRunner creates a new runner struct instance by parsing
// the configuration options, configuring sources, reading lists
// and setting up loggers, etc.
func NewRunner(options *Options) (*Runner, error) {
	options.ConfigureOutput()

	if exists := utils.FileExists(defaultProviderConfigLocation); !exists {
		logger.Debugf("No default provider config file found: %s", defaultProviderConfigLocation)
		logger.Debugf("Creating new default provider config at %s", defaultProviderConfigLocation)

		if err := createProviderConfigYAML(defaultProviderConfigLocation); err != nil {
			logger.Errorf("Could not create provider config file: %s\n", err)
		}
	}

	// Check if the application loading with any provider configuration, then take it
	// Otherwise load the default provider config
	if options.ProviderConfig != "" && utils.FileExists(options.ProviderConfig) {
		logger.Infof("Loading provider config from %s", options.ProviderConfig)
		options.loadProvidersFrom(options.ProviderConfig)
	} else {
		logger.Infof("Loading provider config from the default location: %s", defaultProviderConfigLocation)
		options.loadProvidersFrom(defaultProviderConfigLocation)
	}

	// Default output is stdout
	options.Output = os.Stdout

	// Check if stdin pipe was given
	options.Stdin = utils.HasStdin()

	// configure User Agent
	// if not specified, will set the default User-Agent string
	if options.UserAgent == "" {
		options.UserAgent = fmt.Sprintf("leaker/%s", options.Version)
	}

	r := &Runner{
		options: options,
	}

	// Open the local DB cache. In writable mode, the file is created if
	// missing; in read-only mode, a missing file yields a nil handle
	// (a warning is logged below). A corrupt / incompatible schema is
	// a fatal error.
	dbPath := options.ResolvedDBPath()
	writable := !options.NoWriteDB
	if writable {
		logger.Debugf("Using DB in read-write mode from: %s", dbPath)
	} else {
		logger.Debugf("Using database in read-only mode from: %s", dbPath)
	}
	db, err := OpenLeakerDB(dbPath, writable)
	if err != nil {
		logger.Fatalf("cannot open local DB at %s: %s", dbPath, err)
	}
	if db == nil {
		// Read-only mode, no DB on disk.
		logger.Warnf("local DB at %s does not exist; local search will return no results", dbPath)
	}
	r.leakerDB = db

	// Inject the lookup function into any LocalDB source instance so
	// its Run() can call LeakerDB.Search without importing the runner
	// package (which would create a cycle).
	for _, s := range AllSources {
		if ldb, ok := s.(*sources.LocalDB); ok {
			if db != nil {
				ldb.Lookup = db.Search
			}
		}
	}

	if cfgErr := r.configureSources(); cfgErr != nil {
		return r, cfgErr
	}
	return r, nil
}

func (r *Runner) configureSources() error {
	// lowercase all selected sources
	for i := 0; i < len(r.options.Sources); i++ {
		r.options.Sources[i] = strings.TrimSpace(strings.ToLower(r.options.Sources[i]))
	}

	// check for wrong sources. "all" and "online" are group tokens;
	// every other token must match a real source by name.
	var allSourcesNames []string
	allSourcesNames = append(allSourcesNames, "all", "online")
	for _, source := range AllSources {
		allSourcesNames = append(allSourcesNames, source.Name())
	}
	for _, source := range r.options.Sources {
		if !slices.Contains(allSourcesNames, source) {
			return fmt.Errorf("invalid source \"%s\" specified in -s flag", source)
		}
	}

	hasAll := slices.Contains(r.options.Sources, "all")
	hasOnline := slices.Contains(r.options.Sources, "online")
	hasLocal := slices.Contains(r.options.Sources, sources.LocalSourceName)

	// "all" dominates: every source, including local.
	if hasAll {
		if hasOnline {
			logger.Debug("Source token 'online' is redundant when 'all' is specified")
		}
		logger.Debug("Configuring leaker to use all available sources (online + local)")
		r.scanSources = append(r.scanSources, AllSources[:]...)
		return nil
	}

	// "online" + "local" == "all": every source.
	if hasOnline && hasLocal {
		logger.Debug("Configuring leaker to use all available sources (online + local)")
		r.scanSources = append(r.scanSources, AllSources[:]...)
		return nil
	}

	// "online" alone: every source EXCEPT local.
	if hasOnline {
		logger.Debug("Configuring leaker to use all online sources (excluding local)")
		for _, source := range AllSources {
			if source.Name() != sources.LocalSourceName {
				r.scanSources = append(r.scanSources, source)
			}
		}
		return nil
	}

	// Explicit name list. Match by name exactly as today.
	logger.Debugf("Configuring leaker to use specified sources: %s", strings.Join(r.options.Sources, ", "))
	for _, source := range AllSources {
		if slices.Contains(r.options.Sources, strings.ToLower(source.Name())) {
			r.scanSources = append(r.scanSources, source)
		}
	}
	return nil
}

func (r *Runner) RunEnumeration(ctx context.Context) error {
	var err error

	// parse targets
	t, err := utils.ParseTargets(r.options.Targets, r.options.Stdin)
	if err != nil {
		return err
	}

	// configure output
	outputs := []io.Writer{r.options.Output}

	var file *os.File
	if r.options.OutputFile != "" {
		// by default, leaker will raise an error if the output file is already exist
		// it is done like that to increase data safety, but this behavior can be overwritten with --overwrite
		if r.options.Overwrite {
			file, err = utils.CreateFileWithSafe(r.options.OutputFile, true, true)
		} else {
			file, err = utils.CreateFileWithSafe(r.options.OutputFile, true, false)
		}
		if err != nil {
			return err
		}
		outputs = append(outputs, file)
	}

	return r.EnumerateMultipleTargets(ctx, t, outputs)
}

func (r *Runner) EnumerateMultipleTargets(ctx context.Context, reader io.Reader, writers []io.Writer) error {
	if !r.options.NoFilter {
		logger.Debugf("Results filtering is enabled, leaker will filter results by matching every result to inputted target.")
	} else {
		logger.Debugf("Results filtering is disabled, leaker will not filter any result.")
	}

	scanner := bufio.NewScanner(reader)
	emailRegex := regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`)
	domainRegex := regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)
	phoneRegex := regexp.MustCompile(`^\d{10,15}$`)

	var errs []error
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))

		// Normalize phone input: extract digits from formats like "+7 (995) 234-10-96"
		if r.options.Type == sources.TypePhone {
			line = utils.ExtractPhoneDigits(line)
		}

		// check if valid email, domain, or phone
		isEmail := emailRegex.MatchString(line)
		isDomain := domainRegex.MatchString(line)
		isPhone := phoneRegex.MatchString(line)

		if line == "" ||
			(r.options.Type == sources.TypeEmail && !isEmail) ||
			(r.options.Type == sources.TypeDomain && !isDomain) ||
			(r.options.Type == sources.TypePhone && !isPhone) {
			logger.Infof("Can't parse input as target, skipping: %s", line)
			continue
		}

		// run enumeration for a single line
		if err := r.EnumerateSingleTarget(ctx, line, r.options.Type, r.options.Timeout, writers); err != nil {
			logger.Errorf("error enumerating %s: %s", line, err)
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
