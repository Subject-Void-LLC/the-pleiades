// db.pg.migrate's files: which a directory holds, read and checked before
// any runs.
package pg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sqllint"
)

// migrationName is a migration file's name: a version, then a name.
var migrationName = regexp.MustCompile(`^([0-9]{1,18})_([A-Za-z0-9][A-Za-z0-9_.-]*)\.sql$`)

// migration is one file: its version and name, its text and checksum,
// and what sqllint read in it.
type migration struct {
	version    int64
	name, file string
	text       string
	checksum   string
	directives sqllint.Directives
	statements []sqllint.Statement
}

// label names the file in messages.
func (m migration) label() string { return path.Base(m.file) }

// readMigrations lists dir's .sql files and reads each, refusing a name
// that is not <version>_<name>.sql (a typo is not skipped), a down
// migration (it would otherwise run as an up), and a version two files
// share.
func readMigrations(ctx context.Context, ssh *remoteexec.Conn, dir string) ([]migration, error) {
	info, err := remotefile.Stat(ctx, ssh, dir)
	if err != nil {
		return nil, err
	}
	if info.Kind != remotefile.KindDirectory {
		return nil, fmt.Errorf("%s is not a directory on the device", dir)
	}
	res, err := ssh.RunWithStdinBounded(ctx, "find "+remoteexec.QuoteArg(dir)+" -mindepth 1 -maxdepth 1 -iname '*.sql' -print0", nil, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("listing %s: %s", dir, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	var names []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			names = append(names, path.Base(p))
		}
	}
	if len(names) > maxMigrationFiles {
		return nil, fmt.Errorf("%s holds %d .sql files, over the %d one task reads", dir, len(names), maxMigrationFiles)
	}
	sort.Strings(names)
	byVersion := map[int64]string{}
	var out []migration
	total := 0
	for _, name := range names {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".down.sql") || strings.HasSuffix(lower, ".undo.sql") {
			return nil, fmt.Errorf("%s is a down migration; this applies forward only, so move it out of %s", name, dir)
		}
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("%s is not named <version>_<name>.sql, so it would never run; rename it or move it out of %s", name, dir)
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if other, dup := byVersion[version]; dup {
			return nil, fmt.Errorf("%s and %s are both version %d", other, name, version)
		}
		byVersion[version] = name
		file := path.Join(dir, name)
		data, _, err := remotefile.ReadText(ctx, ssh, file, maxMigrationBytes)
		if err != nil {
			return nil, err
		}
		if total += len(data); total > maxMigrationsBytes {
			return nil, fmt.Errorf("%s's migrations are over the %d bytes one task reads", dir, maxMigrationsBytes)
		}
		stmts, err := sqllint.Split(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		directives, err := sqllint.ParseDirectives(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		out = append(out, migration{
			version: version, name: m[2], file: file, text: string(data),
			checksum: hex.EncodeToString(sum[:]), directives: directives, statements: stmts,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// lintMigrations finds every problem in files before any runs: an empty
// file, a no-transaction file of more than one statement, a transactional
// file that ends its own transaction or holds a statement that cannot run
// in one, and, with additiveOnly, a destructive statement in a file that
// does not say why. It also lists each file that says why.
func lintMigrations(files []migration, additiveOnly bool) ([]string, []any) {
	var problems []string
	destructive := []any{}
	for _, m := range files {
		at := func(s sqllint.Statement) string { return fmt.Sprintf("%s:%d", m.label(), s.Line) }
		if len(m.statements) == 0 {
			problems = append(problems, m.label()+": it holds no statement")
			continue
		}
		if m.directives.NoTransaction {
			if len(m.statements) != 1 {
				problems = append(problems, fmt.Sprintf("%s: a no-transaction file must hold exactly one statement, so a failure cannot leave part of it applied; it holds %d", m.label(), len(m.statements)))
			}
		} else {
			for _, s := range m.statements {
				if sqllint.EndsTransaction(s) {
					problems = append(problems, at(s)+": it ends the transaction the file runs in; each file is its own transaction")
				}
				if what := sqllint.OutsideTransaction(s); what != "" {
					problems = append(problems, fmt.Sprintf("%s: %s cannot run in a transaction; give it a file of its own starting -- pleiades: no-transaction", at(s), what))
				}
			}
		}
		if m.directives.Destructive != "" {
			destructive = append(destructive, map[string]any{"file": m.label(), "reason": m.directives.Destructive})
			continue
		}
		if additiveOnly {
			for _, s := range m.statements {
				if why := sqllint.Destructive(s); why != "" {
					problems = append(problems, fmt.Sprintf("%s: %s (additive_only); if that is meant, say why with -- pleiades: destructive: <why>", at(s), why))
				}
			}
		}
	}
	return problems, destructive
}
