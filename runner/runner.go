package runner

import (
	"bufio"
	"github.com/vflame6/leaker/utils"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
)

type Runner struct {
	options *Options
}

// NewRunner creates a new runner struct instance by parsing
// the configuration options, configuring sources, reading lists
// and setting up loggers, etc.
func NewRunner(options *Options) (*Runner, error) {
	// --list-sources flag
	if options.ListSources {
		listSources(options)
		os.Exit(0)
	}

	if exists := utils.FileExists(defaultProviderConfigLocation); !exists {
		if err := createProviderConfigYAML(defaultProviderConfigLocation); err != nil {
			log.Printf("Could not create provider config file: %s\n", err)
		}
	}

	// Check if the application loading with any provider configuration, then take it
	// Otherwise load the default provider config
	if options.ProviderConfig != "" && utils.FileExists(options.ProviderConfig) {
		log.Printf("Loading provider config from %s", options.ProviderConfig)
		options.loadProvidersFrom(options.ProviderConfig)
	} else {
		log.Printf("Loading provider config from the default location: %s", defaultProviderConfigLocation)
		options.loadProvidersFrom(defaultProviderConfigLocation)
	}

	//options.ConfigureOutput()

	r := &Runner{
		options: options,
	}

	return r, nil
}

func (r *Runner) RunEnumeration() error {
	t, err := utils.ParseTargets(r.options.Targets)
	if err != nil {
		return err
	}

	return r.EnumerateMultipleEmails(t)
}

func (r *Runner) EnumerateMultipleEmails(reader io.Reader) error {
	var err error
	scanner := bufio.NewScanner(reader)
	emailRegex := regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`)

	log.Println("starting email enumeration")

	for scanner.Scan() {
		email := strings.ToLower(strings.TrimSpace(scanner.Text()))

		// check if valid email
		if email == "" || !emailRegex.MatchString(email) {
			continue
		}

		// run enumeration for a single email
		err = r.EnumerateSingleEmail(email, r.options.Timeout)
	}
	if err != nil {
		return err
	}

	log.Println("finished email enumeration")

	return nil
}
