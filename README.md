<h1 align="center">
    <svg xmlns="http://www.w3.org/2000/svg" height="40" viewBox="0 0 640 640"><!--!Font Awesome Free v7.2.0 by @fontawesome - https://fontawesome.com License - https://fontawesome.com/license/free Copyright 2026 Fonticons, Inc.--><path d="M480 272C480 317.9 465.1 360.3 440 394.7L566.6 521.4C579.1 533.9 579.1 554.2 566.6 566.7C554.1 579.2 533.8 579.2 521.3 566.7L394.7 440C360.3 465.1 317.9 480 272 480C157.1 480 64 386.9 64 272C64 157.1 157.1 64 272 64C386.9 64 480 157.1 480 272zM272 416C351.5 416 416 351.5 416 272C416 192.5 351.5 128 272 128C192.5 128 128 192.5 128 272C128 351.5 192.5 416 272 416z"/></svg>
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
  <a href="#configuration">Configuration</a>
</p>

Created by Maksim Radaev/[@vflame6](https://github.com/vflame6)

---

`leaker` is a leak discovery tool that returns valid credential leaks using passive online sources. It supports searching by email, username, domain, keyword, and phone number.

## Features

![leaker](static/leaker_demo.png)

- **9 sources** — aggregates results from multiple leak databases
- **5 search types** — email, username, domain, keyword, phone
- **Deduplication** — removes duplicate results across sources
- **JSONL output** — structured output for pipelines (`-j`)
- **Rate limiting** — built-in per-source rate limits (disable with `-N`)
- **Proxy support** — route traffic through HTTP proxy (`--proxy`)
- **Multiple API keys** — load balancing across keys per source

### Available sources

| Source | API Key | Search Types | Pricing             |
|--------|---------|-------------|---------------------|
| [ProxyNova](https://www.proxynova.com/tools/comb) | No | all | Free                |
| [LeakCheck](https://leakcheck.io/) | Yes | email, username, domain, keyword, phone | Paid                |
| [OSINTLeak](https://osintleak.com/) | Yes | email, username, domain, keyword, phone | Paid                |
| [Intelligence X](https://intelx.io/) | Yes | all | Free tier available |
| [BreachDirectory](https://breachdirectory.org/) | Yes | all (auto-detect) | Free via RapidAPI   |
| [Leak-Lookup](https://leak-lookup.com/) | Yes | email, username, domain, keyword, phone | Paid                |
| [DeHashed](https://dehashed.com/) | Yes | email, username, domain, keyword, phone | Paid                |
| [Snusbase](https://snusbase.com/) | Yes | email, username, domain, keyword, phone | Paid                |
| [LeakSight](https://leaksight.com/) | Yes | email, username, domain, keyword, phone | Paid                |

> **Note:** ProxyNova, Intelligence X, and BreachDirectory accept any search type — the query is passed as-is without type filtering.

## Usage

```shell
leaker -h
```

```yaml
Usage: leaker <command> [flags]

  leaker is a leak discovery tool that returns valid credential leaks for emails,
  using passive online sources.

Flags:
  -h, --help                 Show context-sensitive help.
  -s, --sources=all,...      Specific sources to use for enumeration (default
                             all). Use --list-sources to display all available
                             sources.
      --timeout=30s          Seconds to wait before timing out (default 30s)
  -N, --no-rate-limit        Disable rate limiting (DANGER)
  -j, --json                 Output results as JSONL (one JSON object per line)
      --no-deduplication     Disable deduplication of results across sources
      --no-filter            Disable results filtering, include every result
  -o, --output=STRING        File to write output to
      --overwrite            Force overwrite of existing output file
  -p, --provider-config="provider-config.yml"
                             Provider config file
      --proxy=STRING         HTTP proxy to use with leaker
  -A, --user-agent=STRING    Custom user agent
      --insecure             Disable TLS certificate verification (use with
                             caution)
      --version              Print version of leaker
  -q, --quiet                Suppress output, print results only
  -v, --verbose              Show sources in results output
  -D, --debug                Enable debug mode
  -L, --list-sources         List all available sources

Commands:
  domain      Search by domain name.
  email       Search by email address.
  keyword     Search by keyword.
  phone       Search by phone number.
  username    Search by username.

  Run "leaker <command> --help" for more information on a command.
```

### Examples

Search by email:

```shell
leaker email user@example.com
```

Search by domain using specific sources:

```shell
leaker domain example.com -s leakcheck,dehashed
```

Search by phone number with JSONL output:

```shell
leaker phone +1234567890 -j -o results.jsonl
```

## Installation

`leaker` requires **go1.24** to install successfully.

```shell
go install -v github.com/vflame6/leaker@latest
```

Compiled binaries are available on the [Releases](https://github.com/vflame6/leaker/releases) page.

Build from source:

```shell
go build -o leaker main.go
```

Build with Docker:

```shell
docker build -t leaker .
```

## Configuration

`leaker` generates a `provider-config.yml` file on first launch. Add your API keys there:

```yaml
leakcheck: [YOUR_LEAKCHECK_API_KEY]
osintleak: [YOUR_OSINTLEAK_API_KEY]
intelx: [2.intelx.io:YOUR_INTELX_API_KEY]
breachdirectory: [YOUR_RAPIDAPI_KEY]
leaklookup: [YOUR_LEAKLOOKUP_API_KEY]
dehashed: [YOUR_DEHASHED_API_KEY]
snusbase: [YOUR_SNUSBASE_ACTIVATION_CODE]
leaksight: [YOUR_LEAKSIGHT_TOKEN]
```

Each source accepts a list of API keys for load balancing:

```yaml
leakcheck: [key1, key2, key3]
```

Intelligence X uses `HOST:API_KEY` format to support different tiers:

```yaml
intelx: [free.intelx.io:your-uuid]   # free tier
intelx: [2.intelx.io:your-uuid]      # paid tier
```

> If you wish to buy a LeakCheck subscription, you can support the project by using this invite link: https://leakcheck.io/?ref=486555

## Contributing

Feel free to open an issue if something does not work, or if you have any ideas to improve the tool.
