package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// loadWorld loads the static inventory and parses a YAML runbook into a
// DAG. validate and run both need exactly this pair, so it lives once
// here (Facade over internal/inventory and internal/engine) instead of
// being copied into each subcommand.
//
// This is the composition root's adapter selection for the Phase W4
// Release Gate: it goes through the inventory.Repository port (via
// inventory.NewFileRepository) rather than calling StaticYAMLPlugin
// directly, the same port entRepository implements at Walk tier and
// above. Swapping which Repository is wired in here, not changing any
// caller, is what "selected only by wiring in the composition root"
// means; run.go and validate.go never know or care which one they got.
func loadWorld(dir, runbookPath string, selection engine.TagFilter) ([]pkginventory.InventoryItem, *engine.DAG, error) {
	items, err := loadInventory(dir)
	if err != nil {
		return nil, nil, err
	}

	builder, err := newRunbookBuilder()
	if err != nil {
		return nil, nil, err
	}

	// runbookPath is a CLI argument the invoking user supplies to their own
	// process, crossing no trust boundary the CLI did not already have
	// (the same as `cat` or `ansible-playbook` itself); see Phase W1's own
	// Schema/Injection Hardening item (IMPLEMENTATION.md) for the full
	// reasoning. BuildFromYAMLFile reads runbookPath itself and resolves
	// any import_tasks task's relative file reference against its
	// directory, replacing a manual os.ReadFile + BuildFromYAML pair that
	// had no base directory to give import_tasks (Phase 34).
	dag, err := builder.BuildFromYAMLFile(runbookPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build DAG from %s: %w", runbookPath, err)
	}
	// The builder has already applied the default selection (every task
	// but one tagged never); a --tags or --skip-tags projects it again.
	if !selection.IsZero() {
		if dag, err = engine.Select(dag, selection); err != nil {
			return nil, nil, fmt.Errorf("failed to select tasks from %s: %w", runbookPath, err)
		}
	}

	return items, dag, nil
}

// loadWorldYAML is loadWorld for a runbook built in memory rather than
// read from a file (adhoc's), compiled by the same builder, so it is held
// to exactly the rules a runbook file is. label names it in an error.
func loadWorldYAML(dir, label string, payload []byte) ([]pkginventory.InventoryItem, *engine.DAG, error) {
	items, err := loadInventory(dir)
	if err != nil {
		return nil, nil, err
	}
	builder, err := newRunbookBuilder()
	if err != nil {
		return nil, nil, err
	}
	dag, err := builder.BuildFromYAML(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build DAG from %s: %w", label, err)
	}
	return items, dag, nil
}

// loadInventory loads every item in dir's static inventory through the
// inventory.Repository port, the inventory half of loadWorld. validate
// calls it directly when it checks several runbooks, so the inventory is
// read once rather than once per runbook. A CLI subcommand has no
// cancellation surface of its own yet, so a fresh background context is
// used here rather than threading one through every subcommand's flag
// parsing.
func loadInventory(dir string) ([]pkginventory.InventoryItem, error) {
	ctx := context.Background()

	inventoryPath := filepath.Join(dir, inventory.DefaultInventoryFilename)
	repo := inventory.NewFileRepository(inventoryPath, inventory.NewItemFactory())

	// GetGroup's Selector is Crawl tier's honest no-op: neither Repository
	// implementation has real grouping infrastructure yet (see
	// fileRepository.GetGroup's own doc comment), so the zero value is
	// passed rather than inventing a group concept this call cannot act on.
	it, err := repo.GetGroup(ctx, pkginventory.Selector{})
	if err != nil {
		return nil, fmt.Errorf("failed to load inventory: %w", err)
	}
	defer it.Close()

	var items []pkginventory.InventoryItem
	for it.Next(ctx) {
		items = append(items, it.Item())
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("failed to load inventory: %w", err)
	}
	return items, nil
}

// newRunbookBuilder returns the builder the CLI compiles runbooks with,
// over a fresh CEL evaluator. One builder compiles any number of
// runbooks, which is how validate checks several with one.
func newRunbookBuilder() (*engine.Builder, error) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, fmt.Errorf("failed to init CEL evaluator: %w", err)
	}
	return engine.NewBuilder(eval), nil
}
