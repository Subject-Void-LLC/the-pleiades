// Package main: loading external Collection programs for the three commands
// that read the Collection registry.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// collectionsDirEnv names the directory external Collections are loaded
// from: programs built outside this repository with pkg/external, each
// providing Collection methods of its own. Unset means none are loaded,
// which is every deployment that has not asked for them.
const collectionsDirEnv = "PLEIADES_COLLECTIONS_DIR"

// collectionsReadPathsEnv lists extra paths, separated like PATH, that
// every external Collection program may read, such as a directory of
// files a method uploads. A program is otherwise confined to its own
// directory and the system files it needs (internal/loader), so this is
// the one way to let it see more, and the loader refuses any entry that
// would expose the project's credential store or the home directory.
const collectionsReadPathsEnv = "PLEIADES_COLLECTIONS_READ_PATHS"

// loadExternalCollections loads every external Collection in the directory
// collectionsDirEnv names and registers its methods, so the command that
// called it can validate, document and run them like built-in ones.
//
// It is called by the three commands that read the Collection registry
// (run, validate, doc), before they read it, and by nothing else: a
// command that never looks a method up has no reason to start a program.
// projectDir is the project whose .pleiades credential store the loader
// must keep every program away from.
//
// A directory that fails to load stops the command. A refused program
// never falls back to anything, and a runbook naming one of its methods
// would otherwise fail later with "unknown fqcn", which hides the reason.
//
// The loader logs through a handler on stderr at warning level: what it
// loaded (info) is noise on a terminal whose command prints its own
// output, while its warnings are not. That covers an engine version
// constraint this unreleased build could not check, and a program that
// left a process holding its response channel open.
func loadExternalCollections(ctx context.Context, projectDir string) (*loader.Set, error) {
	dir := os.Getenv(collectionsDirEnv)
	if dir == "" {
		return nil, nil
	}
	store, err := filepath.Abs(filepath.Join(projectDir, ".pleiades"))
	if err != nil {
		return nil, fmt.Errorf("%s: resolving the project's credential store: %w", collectionsDirEnv, err)
	}
	set, err := loader.Load(ctx, dir, loader.Options{
		EngineVersion: version,
		// The project's credential store and master key: no program may be
		// granted a path that reaches them.
		ProtectedPaths: []string{store},
		ReadPaths:      readPaths(os.Getenv(collectionsReadPathsEnv)),
		// The shared masking ruleset, like every handler in this module
		// (internal/archtest's logging rule): a program's warning can quote
		// text, and text can carry a secret.
		Logger: slog.New(slog.NewTextHandler(os.Stderr, redact.Shared().HandlerOptions(slog.LevelWarn))),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", collectionsDirEnv, err)
	}
	return set, nil
}

// readPaths splits a collectionsReadPathsEnv value into its entries,
// dropping empty ones, so an unset or empty variable grants nothing.
func readPaths(value string) []string {
	var paths []string
	for _, p := range filepath.SplitList(value) {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
