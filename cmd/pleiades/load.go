package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// loadWorld loads the static inventory and parses a YAML runbook into a
// DAG. validate and run both need exactly this pair, so it lives once
// here (Facade over internal/inventory and internal/engine) instead of
// being copied into each subcommand.
//
// This is the composition root's adapter selection for the Phase W4
// Release Gate: it goes through the inventory.Repository port (via
// inventory.NewFileRepository) rather than calling StaticYAMLPlugin
// directly, the same port entRepository implements at Crawl tier and
// above. Swapping which Repository is wired in here, not changing any
// caller, is what "selected only by wiring in the composition root"
// means; run.go and validate.go never know or care which one they got.
// A CLI subcommand has no cancellation surface of its own yet, so a fresh
// background context is used here rather than threading one through every
// subcommand's flag parsing.
func loadWorld(dir, runbookPath string) ([]pkginventory.InventoryItem, *engine.DAG, error) {
	ctx := context.Background()

	inventoryPath := filepath.Join(dir, inventory.DefaultInventoryFilename)
	repo := inventory.NewFileRepository(inventoryPath, inventory.NewItemFactory())

	// GetGroup's groupName is Walk tier's honest no-op: neither Repository
	// implementation has real grouping infrastructure yet (see
	// fileRepository.GetGroup's own doc comment), so an empty string is
	// passed rather than inventing a group concept this call cannot act on.
	it, err := repo.GetGroup(ctx, "")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load inventory: %w", err)
	}
	defer it.Close()

	var items []pkginventory.InventoryItem
	for it.Next(ctx) {
		items = append(items, it.Item())
	}
	if err := it.Error(); err != nil {
		return nil, nil, fmt.Errorf("failed to load inventory: %w", err)
	}

	// runbookPath is a CLI argument the invoking user supplies to their own
	// process, crossing no trust boundary the CLI did not already have
	// (the same as `cat` or `ansible-playbook` itself); see Phase W1's own
	// Schema/Injection Hardening item (IMPLEMENTATION.md) for the full
	// reasoning.
	payload, err := os.ReadFile(runbookPath) // #nosec G304 -- intentional, see comment above
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read runbook %s: %w", runbookPath, err)
	}

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init CEL evaluator: %w", err)
	}

	dag, err := engine.NewBuilder(eval).BuildFromYAML(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build DAG from %s: %w", runbookPath, err)
	}

	return items, dag, nil
}
