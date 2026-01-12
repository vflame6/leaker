package cmd

import "fmt"

// AUTHOR of the program
const AUTHOR = "Maksim Radaev (@vflame6)"

// VERSION should be linked to actual tag
const VERSION = "v1.0.2"

// BANNER format string. It is used in PrintBanner function with VERSION
var BANNER = "\n    __           __            \n   / /__  ____ _/ /_____  _____\n  / / _ \\/ __ `/ //_/ _ \\/ ___/\n / /  __/ /_/ / ,< /  __/ /    \n/_/\\___/\\__,_/_/|_|\\___/_/ %s\n\nMade by %s\n\n"

// PrintBanner is a function to print program banner
func PrintBanner() {
	fmt.Printf(BANNER, VERSION, AUTHOR)
}
