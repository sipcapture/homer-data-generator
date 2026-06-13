package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipcapture/homer-data-generator/internal/gen"
)

func TestGenerate_ducklakeMode(t *testing.T) {
	dir := t.TempDir()
	catalog := filepath.Join(dir, "homer_catalog.sqlite")
	dataPath := filepath.Join(dir, "parquet")

	if err := gen.InitCatalog(gen.LakeConfig{
		CatalogPath: catalog,
		DataPath:    dataPath,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := gen.Generate(gen.Options{
		OutputDir:     dataPath,
		CatalogPath:   catalog,
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
	if res.RowsWritten < 2000 {
		t.Fatalf("rows: got %d want >= 2000", res.RowsWritten)
	}
	// 0.01 GiB target must land well above compressible repeat('X') (~few MiB).
	wantMinF := float64(1<<30) * 0.01 * 0.3 // md5 hex still Snappy-compresses slightly
	wantMin := int64(wantMinF)
	if res.BytesWritten < wantMin {
		t.Fatalf("on-disk bytes %d below 40%% of target (%d); payload may be too compressible",
			res.BytesWritten, wantMin)
	}

	var ducklakeFiles []string
	_ = filepath.Walk(filepath.Join(dataPath, "main"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(info.Name(), "ducklake-") && strings.HasSuffix(info.Name(), ".parquet") {
			ducklakeFiles = append(ducklakeFiles, path)
		}
		return nil
	})
	if len(ducklakeFiles) == 0 {
		t.Fatal("expected ducklake-*.parquet files, found none")
	}
	t.Logf("ducklake files: %d, example: %s", len(ducklakeFiles), filepath.Base(ducklakeFiles[0]))

	if _, err := os.Stat(catalog); err != nil {
		t.Fatalf("catalog missing: %v", err)
	}
}
