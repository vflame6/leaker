package cmd

import (
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner"
	"os"
	"time"
)

var CLI struct {
	// INPUT
	Targets string   `arg:"" optional:"" help:"target email or file with emails"`
	Sources []string `short:"s" default:"all" help:"specific sources to use for enumeration (default all). Use --list-sources to display all available sources."`

	// OPTIMIZATION
	Timeout time.Duration `help:"seconds to wait before timing out (default 10s)" default:"10s"`

	// OUTPUT
	Output    string `short:"o" help:"file to write output to"`
	Overwrite bool   `help:"force overwrite of existing file"`

	// CONFIGURATION
	ProviderConfig string `short:"p" help:"provider config file" default:"provider-config.yml"`
	Proxy          string `help:"http proxy to use with leaker"`
	UserAgent      string `short:"A" help:"custom user agent"`

	// DEBUG
	Version     bool `help:"print version of leaker"`
	Quiet       bool `short:"q" help:"suppress output, print results only"`
	Verbose     bool `short:"v" help:"show sources in results output"`
	Debug       bool `short:"D" help:"enable debug mode"`
	ListSources bool `help:"list all available sources"`
}

func Run() {
	_ = kong.Parse(&CLI,
		kong.Name("leaker"),
		kong.Description("leaker is a leak discovery tool that returns valid credential leaks for emails, using passive online sources."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}))

	if CLI.Version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	if !CLI.Quiet {
		PrintBanner()
	}

	options := &runner.Options{
		Debug:          CLI.Debug,
		ListSources:    CLI.ListSources,
		OutputFile:     CLI.Output,
		Overwrite:      CLI.Overwrite,
		ProviderConfig: CLI.ProviderConfig,
		Proxy:          CLI.Proxy,
		Quiet:          CLI.Quiet,
		Sources:        CLI.Sources,
		Targets:        CLI.Targets,
		Timeout:        CLI.Timeout,
		UserAgent:      CLI.UserAgent,
		Verbose:        CLI.Verbose,
		Version:        VERSION,
	}

	r, err := runner.NewRunner(options)
	if err != nil {
		logger.Fatal(err)
	}

	err = r.RunEnumeration()
	if err != nil {
		logger.Fatal(err)
	}
}
