package runner

import "fmt"

func WritePlainResult(verbose bool, source, value string) {
	if verbose {
		fmt.Printf("[%s] %s\n", source, value)
	} else {
		fmt.Printf("%s\n", value)
	}
}
