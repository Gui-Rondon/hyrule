// Command hyrule is Hyrule's Platform CLI.
//
// Per ADR-0004 (website/docs/reference/architecture-decisions.md), this
// starts with scaffolding only — new and service create — and leaves
// dev/test/build/deploy to later rounds: reusing ci/affected and the
// Makefile pattern don't need a CLI wrapper to already be useful.
//
//	hyrule new my-platform
//	cd my-platform
//	hyrule service create articles
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hyrule:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("missing command")
	}
	switch args[0] {
	case "new":
		return runNew(args[1:])
	case "service":
		return runService(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  hyrule new <project>          scaffold a new multi-service project
  hyrule service create <name>  scaffold a service in the current project`)
}
