package runner

import (
	"bufio"
	"github.com/vflame6/leaker/utils"
	"io"
	"log"
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

	r := &Runner{
		options: options,
	}

	//log.Printf("Loading provider config from %s", defaultConfigLocation)
	//options.loadProvidersFrom(defaultConfigLocation)

	return r, nil
}

func (r *Runner) RunEnumeration() error {
	t, err := utils.ParseTargets(r.options.Targets)
	if err != nil {
		return err
	}

	return r.EnumerateEmails(t)
}

func (r *Runner) EnumerateEmails(reader io.Reader) error {
	var err error
	scanner := bufio.NewScanner(reader)
	emailRegex := regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`)

	log.Println("starting enumeration")

	for scanner.Scan() {
		email := strings.ToLower(strings.TrimSpace(scanner.Text()))

		// check if valid email
		if email == "" || !emailRegex.MatchString(email) {
			continue
		}

		// run enumeration for a single email
		err = r.EnumerateSingleEmail(email)
	}
	if err != nil {
		return err
	}

	log.Println("finished enumeration")

	return nil
}
