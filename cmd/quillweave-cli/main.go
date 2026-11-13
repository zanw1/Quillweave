package main

import (
	"os"
	"quillweave/internal/headless"
)

// main starts the console companion with exactly the desktop's shared business and protocol code.
func main() { os.Exit(headless.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
