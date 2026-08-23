// Command pleiades's `inventory sync` subcommand lives here: the
// user-facing surface over the sync plugin port in
// internal/inventory/syncplugin. Like every other subcommand file it
// parses flags and delegates; the reconciliation itself belongs there.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// inventoryCommands maps each `inventory` subcommand to its handler,
// mirroring forge.go's nested dispatch rather than inventing a second
// shape for the same problem.
var inventoryCommands = map[string]commandFunc{
	"sync":    runInventorySync,
	"plugins": runInventoryPlugins,
}

// runInventory dispatches the inventory subcommand family.
func runInventory(args []string) error {
	if len(args) == 0 {
		printInventoryUsage()
		return errUnknownCommand
	}

	switch args[0] {
	case "-h", "--help", "help":
		printInventoryUsage()
		return nil
	}

	cmd, ok := inventoryCommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "pleiades inventory: unknown command %q\n", args[0])
		printInventoryUsage()
		return errUnknownCommand
	}
	return cmd(args[1:])
}

// printInventoryUsage prints the inventory namespace's usage block.
func printInventoryUsage() {
	invSpec, _ := clispec.Find(clispec.Root, "inventory")
	fmt.Fprint(os.Stderr, "usage: pleiades inventory <command> [flags]\n\ncommands:\n")
	fmt.Fprint(os.Stderr, clispec.RenderList(invSpec.Subcommands))
	fmt.Fprintln(os.Stderr, "\nSee docs/ in the repository for the sync plugin model.")
}

// runInventoryPlugins lists every registered sync plugin, so a user can
// discover what --plugin accepts without reading the source.
func runInventoryPlugins(args []string) error {
	fs := flag.NewFlagSet("inventory plugins", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	names := syncplugin.Names()
	if len(names) == 0 {
		// Not a cosmetic case: an empty registry means no plugin package
		// was blank-imported, which is FAILURE_PATTERNS.md #52 happening
		// again rather than a controller with nothing to say.
		return fmt.Errorf("no sync plugins are registered, which means internal/inventory/plugins was not linked in")
	}

	for _, name := range names {
		desc, ok := syncplugin.Lookup(name)
		if !ok {
			continue
		}
		status := string(syncplugin.StatusDeclared)
		if desc.Implemented() {
			status = string(syncplugin.StatusImplemented)
		}
		fmt.Printf("%-18s %-12s %s\n", name, status, desc.Description)

		// A plugin's own settings are printed under it rather than left
		// to be discovered from a Connect-time refusal. The AWS plugin
		// needed a region for its whole existence and there was no way
		// to learn that short of reading its source.
		for _, spec := range desc.Settings {
			requirement := "optional"
			if spec.Required {
				requirement = "required"
			}
			fmt.Printf("  --set %-14s %-12s %s\n", spec.Name+"=...", requirement, spec.Description)
		}
		if desc.RequiresCredentials {
			fmt.Printf("  %-20s %-12s resolved from the project credential store, defaulting to the plugin name\n", "--credential name", "required")
		}
	}
	return nil
}

// settingFlag collects repeated --set key=value flags into the map
// syncplugin.Config.Settings carries. It is a flag.Value rather than a
// single comma-joined string so a value containing a comma is not a
// parsing problem, and so the same flag can be repeated the way a person
// expects it to be.
type settingFlag map[string]string

// String renders the collected settings for flag's usage output. The
// keys are sorted so the text is stable rather than map-ordered.
func (f settingFlag) String() string {
	if len(f) == 0 {
		return ""
	}
	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+f[key])
	}
	return strings.Join(parts, ",")
}

// Set records one key=value pair, refusing a malformed one rather than
// storing an empty key nothing will ever match a declared setting to.
func (f settingFlag) Set(raw string) error {
	key, value, found := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" {
		return fmt.Errorf("--set expects key=value, got %q", raw)
	}
	if _, duplicate := f[key]; duplicate {
		return fmt.Errorf("--set %s given more than once", key)
	}
	f[key] = strings.TrimSpace(value)
	return nil
}

// runInventorySync pulls devices from an external source into the project's
// inventory.
func runInventorySync(args []string) error {
	fs := flag.NewFlagSet("inventory sync", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory holding inventory.yaml")
	pluginName := fs.String("plugin", "", "sync plugin to run (see 'pleiades inventory plugins')")
	endpoint := fs.String("endpoint", "", "upstream base URL, overriding the plugin's default")
	credentialName := fs.String("credential", "", "credential store entry to authenticate with, defaulting to the plugin name")
	pageSize := fs.Int("page-size", 0, "how many records to request per upstream page")
	readOnly := fs.Bool("read-only", false, "refuse every write to the local inventory, reporting what would have changed")
	insecure := fs.Bool("insecure-skip-verify", false, "skip TLS certificate verification against the upstream system")
	settings := settingFlag{}
	fs.Var(settings, "set", "a plugin-specific setting as key=value, repeatable (see 'pleiades inventory plugins')")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pluginName == "" {
		return fmt.Errorf("usage: pleiades inventory sync --plugin <name> [--endpoint url] [--credential name] [--read-only] [--dir .]")
	}

	desc, ok := syncplugin.Lookup(*pluginName)
	if !ok {
		return fmt.Errorf("unknown sync plugin %q, known plugins: %s", *pluginName, strings.Join(syncplugin.Names(), ", "))
	}

	cfg := desc.DefaultConfig
	cfg.Name = desc.Name
	if *endpoint != "" {
		cfg.Endpoint = *endpoint
	}
	if *credentialName != "" {
		cfg.CredentialName = *credentialName
	}
	if *pageSize > 0 {
		cfg.PageSize = *pageSize
	}
	if *insecure {
		cfg.InsecureSkipVerify = true
	}
	if len(settings) > 0 {
		cfg.Settings = settings
	}

	// syncplugin.Open is the ONE construction path, shared with the
	// conformance suite, so a plugin cannot pass its tests while being
	// unreachable here. It checks the descriptor's declared settings and
	// hands every plugin the same Deps, which is what replaced a
	// per-plugin type switch that had grown one arm and needed three.
	plugin, err := syncplugin.Open(desc, cfg, syncplugin.Deps{
		Credentials: credential.NewLazyFileStore(*dir),
	})
	if err != nil {
		return err
	}
	defer func() { _ = plugin.Close() }()

	ctx := context.Background()
	if err := plugin.Connect(ctx, cfg); err != nil {
		return err
	}

	repo := inv.Repository(inv.NewFileRepository(filepath.Join(*dir, "inventory.yaml"), inv.NewItemFactory()))
	if *readOnly {
		repo = inv.NewReadOnlyRepository(repo)
	}

	report, err := plugin.Sync(ctx, repo)
	if err != nil {
		return err
	}

	printReconciliation(report)
	return nil
}

// printReconciliation writes the sync report: a summary line, then every
// device whose outcome an operator has to act on.
//
// Added and unchanged devices are counted but not listed. Quarantined and
// conflicted ones are listed individually with their reason, because those
// are the two outcomes that need a human decision, and a summary count
// alone would make them easy to miss in a fleet of hundreds.
func printReconciliation(report syncplugin.Reconciliation) {
	if wouldAdd, wouldUpdate := report.Count(syncplugin.OutcomeWouldAdd), report.Count(syncplugin.OutcomeWouldUpdate); wouldAdd+wouldUpdate > 0 {
		fmt.Printf("read-only: discovered %d device(s): %d would be added, %d would be updated, %d unchanged, %d quarantined, %d conflicted\n",
			report.Total(), wouldAdd, wouldUpdate,
			report.Count(syncplugin.OutcomeUnchanged),
			report.Count(syncplugin.OutcomeQuarantined),
			report.Count(syncplugin.OutcomeConflict),
		)
		return
	}

	fmt.Printf("discovered %d device(s): %d added, %d updated, %d unchanged, %d quarantined, %d conflicted\n",
		report.Total(),
		report.Count(syncplugin.OutcomeAdded),
		report.Count(syncplugin.OutcomeUpdated),
		report.Count(syncplugin.OutcomeUnchanged),
		report.Count(syncplugin.OutcomeQuarantined),
		report.Count(syncplugin.OutcomeConflict),
	)

	needsAttention := make([]syncplugin.DeviceResult, 0)
	for _, res := range report.Results {
		if res.Outcome == syncplugin.OutcomeQuarantined || res.Outcome == syncplugin.OutcomeConflict {
			needsAttention = append(needsAttention, res)
		}
	}
	if len(needsAttention) == 0 {
		return
	}

	sort.Slice(needsAttention, func(i, j int) bool {
		return needsAttention[i].Name < needsAttention[j].Name
	})
	fmt.Println("\ndevices needing review:")
	for _, res := range needsAttention {
		fmt.Printf("  %-28s %-12s %s\n", res.Name, res.Outcome, res.Reason)
	}
}
