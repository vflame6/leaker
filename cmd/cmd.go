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
	Targets string   `arg:"" optional:"" help:"Target email or file with emails"`
	Sources []string `short:"s" default:"all" help:"Specific sources to use for enumeration (default all). Use --list-sources to display all available sources."`

	// OPTIMIZATION
	Timeout     time.Duration `help:"Seconds to wait before timing out (default 10s)" default:"10s"`
	NoRateLimit bool          `short:"N" help:"Disable rate limiting (DANGER)"`

	// OUTPUT
	Output    string `short:"o" help:"File to write output to"`
	Overwrite bool   `help:"Force overwrite of existing output file"`

	// CONFIGURATION
	ProviderConfig string `short:"p" help:"Provider config file" default:"provider-config.yml"`
	Proxy          string `help:"HTTP proxy to use with leaker"`
	UserAgent      string `short:"A" help:"Custom user agent"`

	// DEBUG
	Version     bool `help:"Print version of leaker"`
	Quiet       bool `short:"q" help:"Suppress output, print results only"`
	Verbose     bool `short:"v" help:"Show sources in results output"`
	Debug       bool `short:"D" help:"Enable debug mode"`
	ListSources bool `short:"L" help:"List all available sources"`
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
		NoRateLimit:    CLI.NoRateLimit,
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
