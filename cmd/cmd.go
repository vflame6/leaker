package cmd

import (
	"github.com/alecthomas/kong"
	"github.com/vflame6/leaker/runner"
	"log"
)

var CLI struct {
	Quiet bool `short:"q" help:"Don't print leaker's beautiful banner."`

	Targets string `arg:"" required:"" help:"Target email or file with emails."`
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
		Targets: CLI.Targets,
	}

	r, err := runner.NewRunner(options)
	if err != nil {
		log.Fatal(err)
	}

	err = r.RunEnumeration()
	if err != nil {
		log.Fatal(err)
	}
}
