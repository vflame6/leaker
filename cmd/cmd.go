package cmd

import (
	"github.com/alecthomas/kong"
	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner"
	"time"
)

var CLI struct {
	Quiet   bool `short:"q" help:"Suppress output. Print results only."`
	Verbose bool `short:"v" help:"Show verbose output."`

	Timeout time.Duration `help:"Timeout for HTTP requests." default:"10s"`

	Targets string `arg:"" optional:"" help:"Target email or file with emails."`

	ProviderConfig string `short:"p" help:"Path to a configuration file." default:"provider-config.yml"`
	ListSources    bool   `help:"List all available sources."`
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

	if !CLI.Quiet {
		PrintBanner()
	}

	options := &runner.Options{
		Targets:        CLI.Targets,
		Timeout:        CLI.Timeout,
		Quiet:          CLI.Quiet,
		Verbose:        CLI.Verbose,
		ListSources:    CLI.ListSources,
		ProviderConfig: CLI.ProviderConfig,
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
