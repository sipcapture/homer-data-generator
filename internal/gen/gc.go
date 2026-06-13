package gen

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// gcOrphanInlineTables drops empty ducklake_inlined_data_* tables left in the
// catalog after flush (upstream duckdb/ducklake#1065). DuckLake rebuilds an
// in-memory stats map over all of them on catalog refresh — RSS grows to GB
// over hundreds of batches if they accumulate.
//
// Must run while DuckDB does not have the catalog ATTACHed (exclusive sqlite access).
func gcOrphanInlineTables(catalogPath string) (int, error) {
	if _, err := os.Stat(catalogPath); os.IsNotExist(err) {
		return 0, nil
	}

	names, err := sqliteLines(catalogPath,
		`SELECT name FROM sqlite_master WHERE type='table' `+
			`AND name LIKE 'ducklake_inlined_data\_%' ESCAPE '\' `+
			`AND name <> 'ducklake_inlined_data_tables';`)
	if err != nil || len(names) == 0 {
		return 0, err
	}

	var q strings.Builder
	for i, n := range names {
		if i > 0 {
			q.WriteString("\nUNION ALL ")
		}
		fmt.Fprintf(&q, `SELECT '%s' WHERE (SELECT count(*) FROM "%s")=0`, n, n)
	}
	empties, err := sqliteLines(catalogPath, q.String())
	if err != nil || len(empties) == 0 {
		return 0, err
	}

	var script strings.Builder
	script.WriteString("BEGIN;\n")
	for _, t := range empties {
		fmt.Fprintf(&script, "DROP TABLE IF EXISTS \"%s\";\n", t)
		fmt.Fprintf(&script, "DELETE FROM ducklake_inlined_data_tables WHERE table_name='%s';\n", t)
	}
	script.WriteString("COMMIT;\n")

	if _, err := runSQLiteCLI(catalogPath, script.String()); err != nil {
		return 0, err
	}
	return len(empties), nil
}

func runSQLiteCLI(catalogPath, sql string) (string, error) {
	cmd := exec.Command("sqlite3", "-batch", "-noheader", catalogPath)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.Output()
	return string(out), err
}

func sqliteLines(catalogPath, sql string) ([]string, error) {
	out, err := runSQLiteCLI(catalogPath, sql)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
