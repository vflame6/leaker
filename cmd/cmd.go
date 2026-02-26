package cmd

import (
	"context"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner"
	"github.com/vflame6/leaker/runner/sources"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var CLI struct {
	// COMMAND
	Domain struct {
		Targets string `arg:"" optional:"" help:"Target domain or file with domains, one per line"`
	} `cmd:"" help:"Search by domain name."`
	Email struct {
		Targets string `arg:"" optional:"" help:"Target email or file with emails, one per line"`
	} `cmd:"" help:"Search by email address."`
	Keyword struct {
		Targets string `arg:"" optional:"" help:"Target keyword or file with keywords, one per line"`
	} `cmd:"" help:"Search by keyword."`
	Username struct {
		Targets string `arg:"" optional:"" help:"Target username or file with usernames, one per line"`
	} `cmd:"" help:"Search by username."`

	// INPUT
	Sources []string `short:"s" default:"all" help:"Specific sources to use for enumeration (default all). Use --list-sources to display all available sources."`

	// OPTIMIZATION
	Timeout     time.Duration `help:"Seconds to wait before timing out (default 30s)" default:"30s"`
	NoRateLimit bool          `short:"N" help:"Disable rate limiting (DANGER)"`

	// OUTPUT
	JSON           bool   `short:"j" help:"Output results as JSONL (one JSON object per line)"`
	ShowDuplicates bool   `help:"Disable deduplication of results across sources"`
	NoFilter       bool   `help:"Disable results filtering, include every result"`
	Output         string `short:"o" help:"File to write output to"`
	Overwrite      bool   `help:"Force overwrite of existing output file"`

	// CONFIGURATION
	ProviderConfig string `short:"p" help:"Provider config file" default:"provider-config.yml"`
	Proxy          string `help:"HTTP proxy to use with leaker"`
	UserAgent      string `short:"A" help:"Custom user agent"`
	Insecure       bool   `help:"Disable TLS certificate verification (use with caution)"`

	// DEBUG
	Version     bool `help:"Print version of leaker"`
	Quiet       bool `short:"q" help:"Suppress output, print results only"`
	Verbose     bool `short:"v" help:"Show sources in results output"`
	Debug       bool `short:"D" help:"Enable debug mode"`
	ListSources bool `short:"L" help:"List all available sources"`
}

func Run() {
	parser, err := kong.New(&CLI,
		kong.Name("leaker"),
		kong.Description("leaker is a leak discovery tool that returns valid credential leaks for emails, using passive online sources."),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}))
	if err != nil {
		panic(err)
	}

	ctx, parseErr := parser.Parse(os.Args[1:])

	// These flags don't require a command; handle them before enforcing one.
	// show version
	if CLI.Version {
		fmt.Println(VERSION)
		os.Exit(0)
	}
	// list available sources
	if CLI.ListSources {
		runner.ListSources()
		os.Exit(0)
	}

	// error if no command is specified
	if parseErr != nil {
		if len(os.Args) == 1 {
			if pe, ok := parseErr.(*kong.ParseError); ok {
				_ = pe.Context.PrintUsage(false)
			}
			os.Exit(0)
		}
		parser.FatalIfErrorf(parseErr)
	}

	// output banner
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
		Insecure:       CLI.Insecure,
		JSON:           CLI.JSON,
		ListSources:    CLI.ListSources,
		ShowDuplicates: CLI.ShowDuplicates,
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

	runCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	r, err := runner.NewRunner(options)
	if err != nil {
		logger.Fatal(err)
	}

	err = r.RunEnumeration(runCtx)
	if err != nil {
		logger.Fatal(err)
	}
}
