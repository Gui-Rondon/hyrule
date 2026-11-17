// Command tags-mcp runs the opt-in local tags pilot over MCP stdio. All owner
// configuration comes from explicit environment/files; stdout is protocol only.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"example.com/standalone-tags-api/internal/tagmcp"
)

func main() {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
			fmt.Fprintln(os.Stderr, "Usage: tags-mcp (MCP stdio; no command-line credentials)")
			fmt.Fprintln(os.Stderr, "Required environment variables:")
			for _, name := range tagmcp.RequiredEnvironment() {
				fmt.Fprintln(os.Stderr, "  "+name)
			}
			return
		}
		fmt.Fprintln(os.Stderr, "tags-mcp: arguments are not accepted; use explicit environment configuration")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := tagmcp.RunCommand(ctx, os.Getenv)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
