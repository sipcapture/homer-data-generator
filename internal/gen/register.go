package gen

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/sipcapture/homer-data-generator/internal/schema"
)

// Register imports legacy raw parquet (data_NNNNN.parquet from generate without
// --catalog) into an existing DuckLake catalog.
//
// When you generate with --catalog, DuckLake updates catalog.sqlite automatically;
// this command is NOT needed.
type RegisterOptions struct {
	CatalogPath string
	DataPath    string
	LakeName    string
	Table       string
}

// Register runs INSERT INTO {lake}.main.{table} SELECT * FROM read_parquet(...)
// for hive-partitioned files under {DataPath}/main/{table}/.
//
// Prerequisites: catalog already exists and the table was created once by homer-core
// (empty table is fine). Stop homer-core before running to avoid catalog locks.
func Register(opts RegisterOptions) error {
	if strings.TrimSpace(opts.CatalogPath) == "" {
		return fmt.Errorf("catalog path is required")
	}
	if strings.TrimSpace(opts.DataPath) == "" {
		return fmt.Errorf("data path is required")
	}
	if opts.LakeName == "" {
		opts.LakeName = "homer_lake"
	}
	if opts.Table == "" {
		opts.Table = schema.CallTable
	}

	glob := filepath.Join(opts.DataPath, "main", opts.Table, "date=*/**/*.parquet")
	if _, err := os.Stat(filepath.Join(opts.DataPath, "main", opts.Table)); os.IsNotExist(err) {
		return fmt.Errorf("no parquet dir at %s", filepath.Join(opts.DataPath, "main", opts.Table))
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	for _, stmt := range []string{
		"LOAD ducklake;",
		"LOAD sqlite;",
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w (run: homer-core --install-extensions)", strings.TrimSpace(stmt), err)
		}
	}

	attach := fmt.Sprintf(
		"ATTACH 'ducklake:sqlite:%s' AS %s (DATA_PATH '%s', AUTOMATIC_MIGRATION TRUE);",
		escapeSQLString(opts.CatalogPath),
		opts.LakeName,
		escapeSQLString(opts.DataPath),
	)
	if _, err := db.Exec(attach); err != nil {
		return fmt.Errorf("attach ducklake: %w", err)
	}

	fqn := fmt.Sprintf("%s.main.%s", opts.LakeName, opts.Table)
	insertSQL := fmt.Sprintf(
		`INSERT INTO %s SELECT * FROM read_parquet('%s', union_by_name=true, hive_partitioning=true)`,
		fqn, escapeSQLString(glob),
	)

	fmt.Printf("Registering parquet into %s ...\n", fqn)
	fmt.Printf("Glob: %s\n", glob)

	result, err := db.Exec(insertSQL)
	if err != nil {
		return fmt.Errorf("insert from parquet: %w", err)
	}
	rows, _ := result.RowsAffected()
	fmt.Printf("Registered %d rows into DuckLake catalog %s\n", rows, opts.CatalogPath)
	return nil
}
