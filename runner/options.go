package runner

import (
	"errors"
	"fmt"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"github.com/vflame6/leaker/utils"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	configDir                     = utils.AppConfigDirOrDefault(".", "leaker")
	defaultProviderConfigLocation = utils.GetEnvOrDefault("LEAKER_PROVIDER_CONFIG", filepath.Join(configDir, "provider-config.yaml"))
)

// Options struct is used to store leaker options. Sort alphabetically
type Options struct {
	Debug          bool
	ListSources    bool
	NoFilter       bool
	NoRateLimit    bool
	Output         io.Writer
	OutputFile     string
	Overwrite      bool
	ProviderConfig string // ProviderConfig contains the location of the provider config file
	Proxy          string
	Quiet          bool
	Sources        []string
	Stdin          bool
	Targets        string
	Timeout        time.Duration
	Type           sources.ScanType
	UserAgent      string
	Verbose        bool
	Version        string
}

func listSources(options *Options) {
	logger.Infof("Current list of available sources. [%d]", len(AllSources))
	logger.Infof("Sources marked with an * require key(s) or token(s) to work.")
	logger.Infof("You can modify %s to configure your keys/tokens.\n", options.ProviderConfig)

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
		logger.Errorf("Could not read providers from %s: %s\n", location, err)
	}
}

// ConfigureOutput configures the output on the screen
func (options *Options) ConfigureOutput() {
	// If the user desires verbose output, show verbose output
	if options.Debug {
		logger.DefaultLogger.SetMaxLevel(logger.LevelVerbose)
	}
	if options.Quiet {
		logger.DefaultLogger.SetMaxLevel(logger.LevelFatal)
	}
}
