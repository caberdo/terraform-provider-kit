// Command tfkit is the shared tooling for the caberdo Terraform providers.
package main

import (
	"io"
	"os"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
)

// version is the reported program version. It stays a variable so that
// GoReleaser can override it at build time with
// -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run keeps main free of logic so the wiring is testable: it hands the
// arguments and streams to the dispatcher together with the build-time version.
func run(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, version, stdout, stderr)
}
