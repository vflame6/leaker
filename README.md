<h1 align="center">
  leaker
</h1>

<h4 align="center">Passive leak enumeration tool.</h4>

<p align="center">
<a href="https://goreportcard.com/report/github.com/vflame6/leaker" target="_blank"><img src="https://goreportcard.com/badge/github.com/vflame6/leaker"></a>
<a href="https://github.com/vflame6/leaker/issues"><img src="https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat"></a>
<a href="https://github.com/vflame6/leaker/releases"><img src="https://img.shields.io/github/release/vflame6/leaker"></a>
<a href="https://t.me/vflame6"><img src="https://img.shields.io/badge/Follow-@vflame6-33a3e1?style=flat&logo=telegram"></a>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#usage">Usage</a> •
  <a href="#installation">Install</a> •
  <a href="#post-installation-instructions">API Setup</a>
</p>

Created by Maksim Radaev/[@vflame6](https://github.com/vflame6)

---

`leaker` is a leak discovery tool that returns valid credential leaks for emails, using passive online sources. 


## Features

![leaker](static/leaker_demo.png)

Available sources: `proxynova`, `leakcheck`.

Available search types: `email`, `username`, `domain`, `keyword`.

## Usage

```shell
leaker -h
```

Here is a help menu for the tool:

```yaml
Usage: leaker <command> [flags]

  leaker is a leak discovery tool that returns valid credential leaks for emails, using passive online sources.

Flags:
  -h, --help                                     Show context-sensitive help.
  -s, --sources=all,...                          Specific sources to use for enumeration (default all). Use --list-sources to display all available sources.
  --timeout=30s                              Seconds to wait before timing out (default 30s)
  -N, --no-rate-limit                            Disable rate limiting (DANGER)
  --no-filter                                Disable results filtering, include every result
  -o, --output=STRING                            File to write output to
  --overwrite                                Force overwrite of existing output file
  -p, --provider-config="provider-config.yml"    Provider config file
  --proxy=STRING                             HTTP proxy to use with leaker
  -A, --user-agent=STRING                        Custom user agent
  --version                                  Print version of leaker
  -q, --quiet                                    Suppress output, print results only
  -v, --verbose                                  Show sources in results output
  -D, --debug                                    Enable debug mode
  -L, --list-sources                             List all available sources

Commands:
  domain      Search by domain name.
  email       Search by email address.
  keyword     Search by keyword.
  username    Search by username.

  Run "leaker <command> --help" for more information on a command.
```

## Installation

`leaker` requires **go1.25** to install successfully.

```shell
go install -v github.com/vflame6/leaker@latest
```

Compiled versions are available on [Release Binaries](https://github.com/vflame6/leaker/releases) page.

To Build:

```
go build -o leaker main.go
```

Build with Docker:

```shell
docker build -t leaker . 
```

### Post Installation Instructions

`leaker` can be used right after the installation, however many sources required API keys to work. View an example configuration file here: https://github.com/vflame6/leaker/blob/main/static/provider-config.yml

The tool will generate a provider configuration file on the first launch, so you can also specify API keys there.

If you wish to buy a LeakCheck subscription, you can support me by using my invite link to do that: https://leakcheck.io/?ref=486555. 

## Contributing

Feel free to open an issue if something does not work, or if you have any issues. New ideas to improve the tool are much appreciated.
