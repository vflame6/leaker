package runner

import (
	"io"
	"os"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/runner/sources"
	"github.com/vflame6/leaker/utils"
)

// openTargets returns the targets of the run, one per line, and a function
// that closes any file it opened.
//
//   - A positional target naming an existing file is read as a list, one
//     target per line; any other value is searched as given; "-" reads stdin.
//   - -l/--list FILE reads one target per line; "-" reads stdin.
//   - Stdin is read only when neither is given (or one of them is "-"), so
//     an open but idle stdin never blocks a run.
func (r *Runner) openTargets() (io.Reader, func(), error) {
	var readers []io.Reader
	var files []*os.File
	closeFiles := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	stdinAdded := false
	addStdin := func() {
		if !stdinAdded {
			readers = append(readers, r.stdin())
			stdinAdded = true
		}
	}
	addFile := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return configErrorf("cannot read targets: %w", err)
		}
		files = append(files, f)
		readers = append(readers, f)
		return nil
	}

	switch positional := strings.TrimSpace(r.options.Targets); {
	case positional == "-":
		addStdin()
	case positional != "" && utils.FileExists(positional):
		logger.Debugf("Reading targets from the file %s", positional)
		if err := addFile(positional); err != nil {
			return nil, nil, err
		}
	case positional != "":
		readers = append(readers, strings.NewReader(positional+"\n"))
	}

	switch list := strings.TrimSpace(r.options.ListFile); list {
	case "":
	case "-":
		addStdin()
	default:
		if err := addFile(list); err != nil {
			closeFiles()
			return nil, nil, err
		}
	}

	if len(readers) == 0 {
		if !r.options.Stdin {
			return nil, nil, configErrorf("no targets: give a target, use -l FILE, or pipe targets on stdin")
		}
		addStdin()
	}
	return io.MultiReader(readers...), closeFiles, nil
}

func (r *Runner) stdin() io.Reader {
	if r.options.StdinReader != nil {
		return r.options.StdinReader
	}
	return os.Stdin
}

// normalizeTarget lower-cases and trims an input line; phone numbers keep
// their digits only ("+7 (995) 234-10-96" → "79952341096").
func normalizeTarget(scanType sources.ScanType, line string) string {
	target := strings.ToLower(strings.TrimSpace(line))
	if scanType == sources.TypePhone {
		target = utils.ExtractPhoneDigits(target)
	}
	return target
}
