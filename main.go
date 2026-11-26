// Codumi: the exact map of your codebase for AI coding agents.
package main

import (
	"os"

	"github.com/codumihq/codumi/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
