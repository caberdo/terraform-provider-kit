// Package cli implements the tfkit command line.
//
// Run is the single place dispatch lives: every subcommand of tfkit adds one
// case to its switch and keeps its logic in its own package.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// usage is printed when tfkit is invoked without a subcommand.
const usage = `tfkit is the shared tooling for the caberdo Terraform providers.

Usage:
  tfkit <command> [arguments]

Commands:
  version   Print the tfkit version.
  coverage  Work with Go coverage profiles.

Run "tfkit <command> -h" for more about a command.
`

// coverageUsage is printed when `tfkit coverage` has no usable subcommand.
const coverageUsage = `Usage:
  tfkit coverage merge --output <path> <input> [input ...]

Commands:
  merge   Merge cover profiles, adding the hit counts of shared blocks.

Run "tfkit coverage <command> -h" for more about a command.
`

// Exit codes returned by Run.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// Run dispatches args to the subcommand it names, writing user-facing output to
// stdout and diagnostics to stderr. It returns the process exit code: 0 on
// success, 1 when a command fails, and 2 for a usage error. The caller passes
// the program version so that main, which owns the -ldflags-injected value,
// stays the only source of it.
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
	case "coverage":
		return runCoverage(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

// runCoverage dispatches the coverage subcommands.
func runCoverage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "coverage requires a subcommand\n\n")
		_, _ = io.WriteString(stderr, coverageUsage)
		return exitUsage
	}

	switch args[0] {
	case "merge":
		return runCoverageMerge(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown coverage command %q\n\n", args[0])
		_, _ = io.WriteString(stderr, coverageUsage)
		return exitUsage
	}
}

// runCoverageMerge implements `tfkit coverage merge`. It only parses arguments
// and moves files; parsing, merging and formatting a profile belongs to the
// coverage package.
func runCoverageMerge(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit coverage merge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "path of the merged profile to write")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, "usage: tfkit coverage merge --output <path> <input> [input ...]\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	inputs := flags.Args()
	// The script this replaces guards with `$# -lt 2`, where the output file
	// counts as the first argument: one output plus at least one input. A
	// missing --output is the same class of mistake.
	if *output == "" || len(inputs) == 0 {
		flags.Usage()
		return exitUsage
	}

	files := make([]*os.File, 0, len(inputs))
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	for _, path := range inputs {
		file, err := os.Open(path)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
			return exitFailure
		}
		files = append(files, file)
	}

	profiles := make([]coverage.Profile, 0, len(files))
	for i, file := range files {
		profile, err := coverage.ParseProfile(file)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "coverage merge: %s: %v\n", inputs[i], err)
			return exitFailure
		}
		profiles = append(profiles, profile)
	}

	merged, err := coverage.Merge(profiles)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}

	out, err := os.Create(*output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}
	if err := coverage.WriteProfile(out, merged); err != nil {
		_ = out.Close()
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}
	if err := out.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}

	_, _ = fmt.Fprintf(stdout, "merged %d profiles into %s\n", len(profiles), *output)
	return exitOK
}

// printUsage writes the usage text to w, for the same reason Run drops the
// write errors above.
func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usage)
}
