// Command asokit tracks where an app ranks in App Store search for a set of
// keywords, over time, using Apple's public iTunes Search API.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "asokit: "+err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return errors.New("a command is required")
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "run":
		return cmdRun(ctx, rest)
	case "once":
		return cmdOnce(ctx, rest)
	case "report":
		return cmdReport(rest)
	case "export":
		return cmdExport(rest)
	case "version", "--version", "-v":
		fmt.Println("asokit " + version)
		return nil
	case "help", "--help", "-h":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `asokit — App Store keyword ranking tracker

Usage:
  asokit <command> [flags]

Commands:
  run       Fetch and store the current ranking proxy for every keyword in the config
  once      One-off: look up a single term for an app and print its position
  report    Show the latest stored position per keyword (use --trend for movement)
  export    Export all stored snapshots as CSV
  version   Print the version
  help      Show this help

Run "asokit <command> --help" for command-specific flags.

Note: positions come from the iTunes Search API's relevance ordering, which is a
proxy for App Store search ranking, not the personalized, ad-influenced ranking
a user sees in the App Store app.
`)
}
