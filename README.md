<h1 align="center">
  leaker
</h1>

<h4 align="center">Passive leak enumeration tool.</h4>

<p align="center">
<a href="https://goreportcard.com/report/github.com/vflame6/leaker" target="_blank"><img src="https://goreportcard.com/badge/github.com/vflame6/leaker"></a>
<a href="https://github.com/vflame6/leaker/issues"><img src="https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat"></a>
<a href="https://github.com/vflame6/leaker/releases"><img src="https://img.shields.io/github/release/vflame6/leaker"></a>
</p>

Created by Maksim Radaev/[@vflame6](https://github.com/vflame6)

---

`leaker` is a leak discovery tool that returns valid credential leaks for emails, using passive online sources. 


## Features

Available sources: `proxynova`, `leakcheck`

## Usage

```shell
leaker -h
```

Here is a help menu for the tool:

```yaml
Usage: leaker [<targets>] [flags]

  leaker is a leak discovery tool that returns valid credential leaks for emails, using passive online sources.

Arguments:
  [<targets>]    Target email or file with emails.

Flags:
  -h, --help                                     Show context-sensitive help.
  -q, --quiet                                    Suppress output. Print results only.
  -v, --verbose                                  Show verbose output.
  --timeout=5s                               Timeout for HTTP requests.
  -p, --provider-config="provider-config.yml"    Path to a configuration file.
  --list-sources                             List all available sources.
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

## Contributing

Feel free to open an issue if something does not work, or if you have any issues. New ideas to improve the tool are much appreciated.
