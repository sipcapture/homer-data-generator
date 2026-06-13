package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sipcapture/homer-data-generator/internal/gen"
	"github.com/sipcapture/homer-data-generator/internal/schema"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "-v", "-version", "--version":
			PrintVersion()
			return
		}
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "generate":
		os.Exit(runGenerate(os.Args[2:]))
	case "init-catalog":
		os.Exit(runInitCatalog(os.Args[2:]))
	case "compact":
		os.Exit(runCompact(os.Args[2:]))
	case "register":
		os.Exit(runRegister(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func lakeFlags(fs *flag.FlagSet) (catalog, dataPath, lake *string) {
	catalog = fs.String("catalog", "", "DuckLake catalog.sqlite (required for Homer-compatible output)")
	dataPath = fs.String("data-path", "/data/homer/parquet", "parquet data root (storage.ducklake.data_path)")
	lake = fs.String("lake", "homer_lake", "DuckLake attach name")
	return
}

func runGenerate(args []string) int {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	catalog, dataPath, lake := lakeFlags(fs)
	output := fs.String("output", "", "alias for --data-path (legacy)")
	days := fs.Int("days", 14, "calendar days of data")
	targetGB := fs.Float64("target-gb", 80, "approximate total parquet size in GiB")
	rowsPerFile := fs.Int("rows-per-file", 25000, "rows per insert batch (≈ one parquet file after flush)")
	filesPerDay := fs.Int("files-per-day", 32, "batches per day partition")
	payloadBytes := fs.Int("payload-bytes", 0, "SIP payload size (0 = auto from target-gb)")
	incompressible := fs.Bool("incompressible-payload", false, "maximal disk size (slow string_agg md5)")
	repeatX := fs.Bool("repeat-x-payload", false, "repeat('X') only — fast smoke, ~2 GiB on disk for target-gb 80")
	startDate := fs.String("start-date", "", "first partition YYYY-MM-DD (default: today-days UTC)")
	seedCallID := fs.String("seed-call-id", "9b9558fa657d11f1aba1000c29796214@91.102.10.105", "Call-ID for search repro rows")
	seedRatio := fs.Float64("seed-call-ratio", 0.001, "fraction of rows with seed-call-id")
	compactEnd := fs.Bool("compact", false, "run ducklake_merge_adjacent_files after generate")
	noFlush := fs.Bool("no-flush-each-batch", false, "skip ducklake_flush_inlined_data per batch")
	memoryLimit := fs.String("memory-limit", "", "DuckDB memory_limit (default: 8GB)")
	tempDir := fs.String("temp-directory", "", "DuckDB spill dir (default: <catalog_dir>/.duckdb_spill)")
	threads := fs.Int("threads", 0, "DuckDB threads (default: 4)")
	reopenEvery := fs.Int("reopen-every", 0, "close/reopen DuckDB every N batches (0 = auto, max 200)")
	maxBatchMB := fs.Int("max-batch-mb", 1536, "max RAM per INSERT batch (auto-tunes rows-per-file)")
	table := fs.String("table", schema.CallTable, "table name")
	_ = fs.Parse(args)

	dp := *dataPath
	if *output != "" {
		dp = *output
	}

	var start time.Time
	if *startDate != "" {
		parsed, err := time.Parse("2006-01-02", *startDate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid start-date: %v\n", err)
			return 1
		}
		start = parsed.UTC()
	} else {
		start = time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -*days)
	}

	_, err := gen.Generate(gen.Options{
		OutputDir:      dp,
		CatalogPath:    *catalog,
		LakeName:       *lake,
		Days:           *days,
		TargetGB:       *targetGB,
		RowsPerFile:    *rowsPerFile,
		FilesPerDay:    *filesPerDay,
		PayloadBytes:        *payloadBytes,
		CompressiblePayload: !*incompressible,
		RepeatXPayload:      *repeatX,
		StartDate:      start,
		SeedCallID:     *seedCallID,
		SeedCallRatio:  *seedRatio,
		Table:          *table,
		FlushEachBatch: !*noFlush,
		CompactAtEnd:   *compactEnd,
		MemoryLimit:    *memoryLimit,
		TempDirectory:  *tempDir,
		Threads:        *threads,
		ReopenEvery:    *reopenEvery,
		MaxBatchMB:     *maxBatchMB,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate failed: %v\n", err)
		return 1
	}
	return 0
}

func runInitCatalog(args []string) int {
	fs := flag.NewFlagSet("init-catalog", flag.ExitOnError)
	catalog, dataPath, lake := lakeFlags(fs)
	table := fs.String("table", schema.CallTable, "table to create")
	_ = fs.Parse(args)

	if *catalog == "" {
		fmt.Fprintln(os.Stderr, "init-catalog: --catalog is required")
		return 1
	}
	if err := gen.InitCatalog(gen.LakeConfig{
		CatalogPath: *catalog,
		DataPath:    *dataPath,
		LakeName:    *lake,
		Table:       *table,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "init-catalog failed: %v\n", err)
		return 1
	}
	return 0
}

func runCompact(args []string) int {
	fs := flag.NewFlagSet("compact", flag.ExitOnError)
	catalog, dataPath, lake := lakeFlags(fs)
	table := fs.String("table", schema.CallTable, "table")
	maxFiles := fs.Int("max-compacted-files", 100, "ducklake_merge_adjacent_files limit")
	_ = fs.Parse(args)

	if *catalog == "" {
		fmt.Fprintln(os.Stderr, "compact: --catalog is required")
		return 1
	}
	if err := gen.Compact(gen.CompactOptions{
		LakeConfig: gen.LakeConfig{
			CatalogPath: *catalog,
			DataPath:    *dataPath,
			LakeName:    *lake,
			Table:       *table,
		},
		MaxCompactedFiles: *maxFiles,
		FlushFirst:        true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "compact failed: %v\n", err)
		return 1
	}
	return 0
}

func runRegister(args []string) int {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	catalog, dataPath, lake := lakeFlags(fs)
	table := fs.String("table", schema.CallTable, "table to import")
	_ = fs.Parse(args)

	if *catalog == "" {
		fmt.Fprintln(os.Stderr, "register: --catalog is required")
		return 1
	}
	if err := gen.Register(gen.RegisterOptions{
		CatalogPath: *catalog,
		DataPath:    *dataPath,
		LakeName:    *lake,
		Table:       *table,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "register failed: %v\n", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintf(os.Stderr, `homer-data-generator — synthetic DuckLake data for Homer OOM testing

Usage:
  homer-data-generator -v, --version     Show version and exit
  homer-data-generator init-catalog      --catalog PATH --data-path PATH
  homer-data-generator generate          --catalog PATH --data-path PATH [flags]
  homer-data-generator compact           --catalog PATH --data-path PATH
  homer-data-generator register          --catalog PATH --data-path PATH  (legacy raw parquet only)

Homer-compatible flow (ducklake-{uuid}.parquet + catalog.sqlite):
  ./bin/homer-data-generator init-catalog \
    --catalog /data/homer/homer_catalog.sqlite \
    --data-path /data/homer/parquet

  ./bin/homer-data-generator generate \
    --catalog /data/homer/homer_catalog.sqlite \
    --data-path /data/homer/parquet \
    --target-gb 80 --days 14

  # optional: merge small files (like CompactionService)
  ./bin/homer-data-generator compact --catalog /data/homer/homer_catalog.sqlite --data-path /data/homer/parquet

Point homer.json storage.ducklake.catalog_path + data_path at the same paths.
Stop homer-core while generating. Start homer-core — search works immediately.

Smoke (~50 MiB):
  ./bin/homer-data-generator generate --catalog /tmp/cat.sqlite --data-path /tmp/parquet \
    --target-gb 0.05 --days 2 --files-per-day 4

`)
}
