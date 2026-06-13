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

// LakeConfig opens or creates a DuckLake catalog + data path.
type LakeConfig struct {
	CatalogPath string
	DataPath    string
	LakeName    string
	Table       string
}

// Lake is an attached DuckLake session (catalog + parquet root).
type Lake struct {
	db  *sql.DB
	cfg LakeConfig
	fqn string
}

// InitCatalog creates catalog.sqlite (if missing), attaches the lake, and
// ensures hep_proto_1_call exists with date partitioning (Homer layout).
func InitCatalog(cfg LakeConfig) error {
	lake, err := openLake(cfg)
	if err != nil {
		return err
	}
	defer lake.Close()
	fmt.Printf("Catalog ready: %s\n", cfg.CatalogPath)
	fmt.Printf("Data path:     %s\n", cfg.DataPath)
	fmt.Printf("Table:         %s\n", lake.fqn)
	return nil
}

func openLake(cfg LakeConfig) (*Lake, error) {
	cfg = cfg.withDefaults()
	if err := os.MkdirAll(cfg.DataPath, 0o755); err != nil {
		return nil, err
	}
	if dir := filepath.Dir(cfg.CatalogPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	for _, stmt := range []string{
		"SET threads TO 4",
		"SET preserve_insertion_order = false",
		"LOAD ducklake",
		"LOAD sqlite",
	} {
		if _, err := db.Exec(stmt + ";"); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w (run: homer-core --install-extensions)", stmt, err)
		}
	}

	attach := fmt.Sprintf(
		"ATTACH 'ducklake:sqlite:%s' AS %s (DATA_PATH '%s', AUTOMATIC_MIGRATION TRUE)",
		escapeSQLString(cfg.CatalogPath),
		cfg.LakeName,
		escapeSQLString(cfg.DataPath),
	)
	if _, err := db.Exec(attach); err != nil {
		db.Close()
		return nil, fmt.Errorf("attach ducklake: %w", err)
	}

	lake := &Lake{
		db:  db,
		cfg: cfg,
		fqn: fmt.Sprintf("%s.main.%s", cfg.LakeName, cfg.Table),
	}
	if err := lake.ensureCallTable(); err != nil {
		db.Close()
		return nil, err
	}
	return lake, nil
}

func (c LakeConfig) withDefaults() LakeConfig {
	if c.LakeName == "" {
		c.LakeName = "homer_lake"
	}
	if c.Table == "" {
		c.Table = schema.CallTable
	}
	return c
}

func (l *Lake) Close() error {
	return l.db.Close()
}

func (l *Lake) ensureCallTable() error {
	meta := fmt.Sprintf("__ducklake_metadata_%s", l.cfg.LakeName)
	var existed int
	_ = l.db.QueryRow(fmt.Sprintf(
		`SELECT count(*) FROM %s.ducklake_table WHERE schema_name = 'main' AND table_name = ?`,
		meta,
	), l.cfg.Table).Scan(&existed)

	createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", l.fqn, schema.CallCreateSQL)
	if _, err := l.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create table %s: %w", l.fqn, err)
	}
	if existed == 0 {
		for _, ddl := range []string{
			fmt.Sprintf("ALTER TABLE %s SET PARTITIONED BY (date)", l.fqn),
			fmt.Sprintf("ALTER TABLE %s SET SORTED BY (timestamp ASC)", l.fqn),
		} {
			if _, err := l.db.Exec(ddl); err != nil {
				return fmt.Errorf("configure table %s: %w", l.fqn, err)
			}
		}
	}
	return nil
}

// InsertStaging copies rows from staging into the DuckLake table. DuckLake writes
// main/{table}/date=YYYY-MM-DD/ducklake-{uuid}.parquet and updates catalog.sqlite.
func (l *Lake) InsertStaging(staging string) (int64, error) {
	res, err := l.db.Exec(fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", l.fqn, staging))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FlushInlined persists inlined catalog rows to parquet (same as compaction prelude).
func (l *Lake) FlushInlined() error {
	_, err := l.db.Exec(fmt.Sprintf("CALL ducklake_flush_inlined_data('%s')", l.cfg.LakeName))
	return err
}

// MergeAdjacent runs ducklake_merge_adjacent_files for the table.
func (l *Lake) MergeAdjacent(maxCompactedFiles int) error {
	if maxCompactedFiles <= 0 {
		maxCompactedFiles = 100
	}
	sql := fmt.Sprintf(
		`CALL ducklake_merge_adjacent_files('%s', '%s', schema => 'main', max_compacted_files => %d)`,
		l.cfg.LakeName, l.cfg.Table, maxCompactedFiles,
	)
	_, err := l.db.Exec(sql)
	return err
}

// ParquetStats walks the data path and sums ducklake-*.parquet sizes.
func (l *Lake) ParquetStats() (files int, bytes int64, err error) {
	root := filepath.Join(l.cfg.DataPath, "main", l.cfg.Table)
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".parquet") && strings.HasPrefix(info.Name(), "ducklake-") {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes, err
}

func (l *Lake) RowCount() (int64, error) {
	var n int64
	err := l.db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", l.fqn)).Scan(&n)
	return n, err
}
