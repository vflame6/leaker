package runner

import (
	"bufio"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

type Runner struct {
	options *Options
}

// NewRunner creates a new runner struct instance by parsing
// the configuration options, configuring sources, reading lists
// and setting up loggers, etc.
func NewRunner(options *Options) (*Runner, error) {
	options.ConfigureOutput()

	// --list-sources flag
	if options.ListSources {
		listSources(options)
		os.Exit(0)
	}

	if exists := utils.FileExists(defaultProviderConfigLocation); !exists {
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

	r := &Runner{
		options: options,
	}

	r.configureSources()

	return r, nil
}

func (r *Runner) configureSources() {
	// check if all sources are specified
	if slices.Contains(r.options.Sources, "all") {
		// add all sources
		for _, source := range AllSources {
			ScanSources = append(ScanSources, source)
		}
		return
	}

	// lowercase all selected sources
	for i := 0; i < len(r.options.Sources); i++ {
		r.options.Sources[i] = strings.ToLower(r.options.Sources[i])
	}

	// add selected sources
	for _, source := range AllSources {
		if slices.Contains(r.options.Sources, strings.ToLower(source.Name())) {
			ScanSources = append(ScanSources, source)
		}
	}
}

func (r *Runner) RunEnumeration() error {
	var err error

	// parse targets
	t, err := utils.ParseTargets(r.options.Targets)
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
			file, err = utils.CreateFile(r.options.OutputFile, true)
		} else {
			file, err = utils.CreateFileWithSafe(r.options.OutputFile, true)
		}
		if err != nil {
			return err
		}
		outputs = append(outputs, file)
	}

	logger.Debugf("starting email enumeration from %s", r.options.Targets)
	return r.EnumerateMultipleEmails(t, outputs)
}

func (r *Runner) EnumerateMultipleEmails(reader io.Reader, writers []io.Writer) error {
	var err error
	scanner := bufio.NewScanner(reader)
	emailRegex := regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`)

	for scanner.Scan() {
		email := strings.ToLower(strings.TrimSpace(scanner.Text()))

		// check if valid email
		if email == "" || !emailRegex.MatchString(email) {
			continue
		}

		// run enumeration for a single email
		err = r.EnumerateSingleEmail(email, r.options.Timeout, writers)
	}
	if err != nil {
		return err
	}

	logger.Info("finished email enumeration")
	return nil
}
