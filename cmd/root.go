package cmd

import (
	"context"
	"fmt"
	"strings"
)

const usage = `gocode-router is a secure OpenAI-compatible proxy.

Usage:
  gocode-router serve [flags]

Commands:
  serve    Start the HTTP server

Flags:
  -h, --help  Show this help message`

// Execute runs the CLI dispatcher with the provided arguments.
func Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return printUsage()
	}

	switch args[0] {
	case "serve":
		return serve(ctx, args[1:])
	case "help", "-h", "--help":
		return printUsage()
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func printUsage() error {
	fmt.Println(strings.TrimSpace(usage))
	return nil
}
