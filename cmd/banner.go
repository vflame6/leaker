package cmd

import (
	"fmt"
	"runtime/debug"
)

// AUTHOR of the program
const AUTHOR = "Maksim Radaev (@vflame6)"

// VERSION should be linked to actual tag
var VERSION = "dev"

// BANNER format string. It is used in PrintBanner function with VERSION
var BANNER = "\n    __           __            \n   / /__  ____ _/ /_____  _____\n  / / _ \\/ __ `/ //_/ _ \\/ ___/\n / /  __/ /_/ / ,< /  __/ /    \n/_/\\___/\\__,_/_/|_|\\___/_/ %s\n\nMade by %s\n\n"

func init() {
	if VERSION == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			VERSION = info.Main.Version
		}
	}
}

// PrintBanner is a function to print program banner
func PrintBanner() {
	fmt.Printf(BANNER, VERSION, AUTHOR)
}
