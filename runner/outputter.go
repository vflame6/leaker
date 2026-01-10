package runner

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

func WritePlainResult(writer io.Writer, verbose bool, source, value string) error {
	var result string
	bufwriter := bufio.NewWriter(writer)

	if verbose {
		result = fmt.Sprintf("[%s] %s\n", source, value)
	} else {
		result = fmt.Sprintf("%s\n", value)
	}

	_, err := bufwriter.WriteString(result)
	if err != nil {
		if flushErr := bufwriter.Flush(); flushErr != nil {
			return errors.Join(err, flushErr)
		}
		return err
	}
	return bufwriter.Flush()
}
