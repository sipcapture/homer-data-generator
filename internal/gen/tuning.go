package gen

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultSpillDirectory returns <catalog_dir>/.duckdb_spill for DuckDB disk spill.
func defaultSpillDirectory(catalogPath string) string {
	dir := strings.TrimSpace(filepath.Dir(catalogPath))
	if dir == "" || dir == "." {
		return ""
	}
	return filepath.Join(dir, ".duckdb_spill")
}

func applyDuckDBTuning(db *sql.DB, threads int, memoryLimit, tempDirectory string) error {
	if threads > 0 {
		if _, err := db.Exec(fmt.Sprintf("SET threads = %d", threads)); err != nil {
			return fmt.Errorf("SET threads: %w", err)
		}
	}
	if s := strings.TrimSpace(memoryLimit); s != "" {
		safe := strings.ReplaceAll(s, "'", "''")
		if _, err := db.Exec(fmt.Sprintf("SET memory_limit = '%s'", safe)); err != nil {
			return fmt.Errorf("SET memory_limit: %w", err)
		}
	}
	if s := strings.TrimSpace(tempDirectory); s != "" {
		if err := os.MkdirAll(s, 0o755); err != nil {
			return fmt.Errorf("temp_directory mkdir: %w", err)
		}
		safe := strings.ReplaceAll(s, "'", "''")
		if _, err := db.Exec(fmt.Sprintf("SET temp_directory = '%s'", safe)); err != nil {
			return fmt.Errorf("SET temp_directory: %w", err)
		}
	}
	if _, err := db.Exec("SET preserve_insertion_order = false"); err != nil {
		return fmt.Errorf("SET preserve_insertion_order: %w", err)
	}
	return nil
}
