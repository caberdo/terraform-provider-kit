// Package cli implements the tfkit command line.
//
// Run is the single place dispatch lives: every subcommand of tfkit adds one
// case to its switch and keeps its logic in its own package.
package cli

import (
	"fmt"
	"io"
)

// usage is printed when tfkit is invoked without a subcommand.
const usage = `tfkit is the shared tooling for the caberdo Terraform providers.

Usage:
  tfkit <command> [arguments]

Commands:
  version   Print the tfkit version.

Run "tfkit <command> -h" for more about a command.
`

// Exit codes returned by Run.
const (
	exitOK    = 0
	exitUsage = 2
)

// Run dispatches args to the subcommand it names, writing user-facing output to
// stdout and diagnostics to stderr. It returns the process exit code: 0 on
// success and 2 for a usage error. The caller passes the program version so
// that main, which owns the -ldflags-injected value, stays the only source of
// it.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "version":
		// A failed write cannot be reported through an exit code that is
		// reserved for the command itself, so it is deliberately dropped.
		_, _ = fmt.Fprintf(stdout, "tfkit %s\n", version)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

// printUsage writes the usage text to w, for the same reason Run drops the
// write errors above.
func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usage)
}
