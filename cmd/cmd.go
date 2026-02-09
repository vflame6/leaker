package cmd

import (
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner"
	"github.com/vflame6/leaker/runner/sources"
	"os"
	"time"
)

var CLI struct {
	// COMMAND
	Email struct {
		Targets string `arg:"" optional:"" help:"Target email or file with emails, one per line"`
	} `cmd:"" help:"Search by email address."`
	Username struct {
		Targets string `arg:"" optional:"" help:"Target username or file with usernames, one per line"`
	} `cmd:"" help:"Search by username."`
	Domain struct {
		Targets string `arg:"" optional:"" help:"Target domain or file with domains, one per line"`
	} `cmd:"" help:"Search by domain name."`
	Keyword struct {
		Targets string `arg:"" optional:"" help:"Target keyword or file with keywords, one per line"`
	} `cmd:"" help:"Search by keyword."`

	// INPUT
	Sources []string `short:"s" default:"all" help:"Specific sources to use for enumeration (default all). Use --list-sources to display all available sources."`

	// OPTIMIZATION
	Timeout     time.Duration `help:"Seconds to wait before timing out (default 30s)" default:"30s"`
	NoRateLimit bool          `short:"N" help:"Disable rate limiting (DANGER)"`

	// OUTPUT
	NoFilter  bool   `help:"Disable results filtering, include every result"`
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
	ctx := kong.Parse(&CLI,
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
	default:
		logger.Fatalf("Unknown command: %s", ctx.Command())
	}

	options := &runner.Options{
		Debug:          CLI.Debug,
		ListSources:    CLI.ListSources,
		NoFilter:       CLI.NoFilter,
		NoRateLimit:    CLI.NoRateLimit,
		OutputFile:     CLI.Output,
		Overwrite:      CLI.Overwrite,
		ProviderConfig: CLI.ProviderConfig,
		Proxy:          CLI.Proxy,
		Quiet:          CLI.Quiet,
		Sources:        CLI.Sources,
		Targets:        targets,
		Timeout:        CLI.Timeout,
		Type:           scanType,
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
