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
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "generate":
		os.Exit(runGenerate(os.Args[2:]))
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

func runGenerate(args []string) int {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	output := fs.String("output", "./parquet", "data root (creates main/hep_proto_1_call/date=.../*.parquet)")
	days := fs.Int("days", 14, "calendar days of data")
	targetGB := fs.Float64("target-gb", 80, "approximate total parquet size in GiB")
	rowsPerFile := fs.Int("rows-per-file", 25000, "rows per parquet file")
	filesPerDay := fs.Int("files-per-day", 32, "parquet files per day partition (many small files ≈ production)")
	payloadBytes := fs.Int("payload-bytes", 0, "SIP payload column size (0 = auto from target-gb)")
	startDate := fs.String("start-date", "", "first partition date YYYY-MM-DD (default: today-days UTC)")
	seedCallID := fs.String("seed-call-id", "9b9558fa657d11f1aba1000c29796214@91.102.10.105", "Call-ID/CID injected in seed-call-ratio rows for LIKE search tests")
	seedRatio := fs.Float64("seed-call-ratio", 0.001, "fraction of rows using seed-call-id (0..1)")
	table := fs.String("table", schema.CallTable, "table name under main/")
	_ = fs.Parse(args)

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
		OutputDir:     *output,
		Days:          *days,
		TargetGB:      *targetGB,
		RowsPerFile:   *rowsPerFile,
		FilesPerDay:   *filesPerDay,
		PayloadBytes:  *payloadBytes,
		StartDate:     start,
		SeedCallID:    *seedCallID,
		SeedCallRatio: *seedRatio,
		Table:         *table,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate failed: %v\n", err)
		return 1
	}
	return 0
}

func runRegister(args []string) int {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	catalog := fs.String("catalog", "/data/homer/homer_catalog.sqlite", "DuckLake sqlite catalog path")
	dataPath := fs.String("data-path", "./parquet", "parquet data root (same as generate --output)")
	lake := fs.String("lake", "homer_lake", "attached lake name")
	table := fs.String("table", schema.CallTable, "table to import")
	_ = fs.Parse(args)

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
	fmt.Fprintf(os.Stderr, `homer-data-generator — synthetic DuckLake parquet for Homer load/OOM testing

Usage:
  homer-data-generator generate [flags]
  homer-data-generator register [flags]

Generate ~80 GiB of hep_proto_1_call over 14 days (default):
  go run . generate --output /data/homer/parquet --target-gb 80 --days 14

Quick smoke (~50 MiB):
  go run . generate --output ./parquet-smoke --target-gb 0.05 --days 2 --files-per-day 4

Register into existing Homer catalog (stop homer-core first):
  go run . register --catalog /data/homer/homer_catalog.sqlite --data-path /data/homer/parquet

Or use homer-core after generate:
  homer-core ducklake compaction --compaction-recover -c homer.json

Then point storage.ducklake.data_path at the parquet directory and search with:
  call_id = 9b9558fa657d11f1aba1000c29796214@91.102.10.105  +  14 day range

`)
}
