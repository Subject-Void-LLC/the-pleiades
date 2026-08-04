package main

import (
	"flag"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// runInit scaffolds a new Walk-tier project: a static inventory file, a
// starter runbook, and a README. All the actual file-writing logic lives
// in inventory.Scaffold; this function only parses flags and prints what
// happened.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory to scaffold")
	if err := fs.Parse(args); err != nil {
		return err
	}

	created, err := inventory.Scaffold(*dir)
	if err != nil {
		return err
	}

	if len(created) == 0 {
		fmt.Println("project already initialized, nothing to do")
		return nil
	}

	fmt.Println("created:")
	for _, path := range created {
		fmt.Println("  " + path)
	}
	return nil
}
