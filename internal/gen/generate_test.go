package gen_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/sipcapture/homer-data-generator/internal/gen"
)

func TestGenerate_smoke(t *testing.T) {
	dir := t.TempDir()
	res, err := gen.Generate(gen.Options{
		OutputDir:     dir,
		Days:          2,
		TargetGB:      0.01,
		RowsPerFile:   500,
		FilesPerDay:   2,
		SeedCallID:    "seed-call@test",
		SeedCallRatio: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesWritten != 4 {
		t.Fatalf("files: got %d want 4", res.FilesWritten)
	}
	if res.RowsWritten != 2000 {
		t.Fatalf("rows: got %d want 2000", res.RowsWritten)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "main", "hep_proto_1_call", "date=*", "*.parquet"))
	if len(matches) != 4 {
		t.Fatalf("parquet files on disk: got %d", len(matches))
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	glob := filepath.Join(dir, "main", "hep_proto_1_call", "date=*/**/*.parquet")
	var n, seed int
	q := `SELECT count(*), count(*) filter (where session_id = 'seed-call@test') FROM read_parquet('` + glob + `', hive_partitioning=true)`
	if err := db.QueryRow(q).Scan(&n, &seed); err != nil {
		t.Fatal(err)
	}
	if n != 2000 {
		t.Fatalf("read_parquet rows: got %d", n)
	}
	if seed < 10 {
		t.Fatalf("expected ~20 seed rows at 1%%, got %d", seed)
	}
	_ = os.RemoveAll(dir)
}
