package runner

import (
	"errors"
	"fmt"
	"github.com/vflame6/leaker/utils"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	configDir                     = utils.AppConfigDirOrDefault(".", "leaker")
	defaultProviderConfigLocation = utils.GetEnvOrDefault("LEAKER_PROVIDER_CONFIG", filepath.Join(configDir, "provider-config.yaml"))
)

type Options struct {
	Quiet          bool
	Verbose        bool
	Targets        string
	Timeout        time.Duration
	ProviderConfig string // ProviderConfig contains the location of the provider config file
	ListSources    bool
}

func listSources(options *Options) {
	fmt.Printf("Current list of available sources. [%d]\n", len(AllSources))
	fmt.Printf("Sources marked with an * require key(s) or token(s) to work.\n")
	fmt.Printf("You can modify %s to configure your keys/tokens.\n\n", options.ProviderConfig)

	for _, source := range AllSources {
		sourceName := source.Name()
		if source.NeedsKey() {
			fmt.Printf("%s *\n", sourceName)
		} else {
			fmt.Printf("%s\n", sourceName)
		}
	}
}

// loadProvidersFrom runs the app with source config
func (options *Options) loadProvidersFrom(location string) {
	if err := UnmarshalFrom(location); err != nil && (!strings.Contains(err.Error(), "file doesn't exist") || errors.Is(err, os.ErrNotExist)) {
		fmt.Printf("Could not read providers from %s: %s\n", location, err)
	}
}
