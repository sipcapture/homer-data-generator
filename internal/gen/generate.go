package gen

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/sipcapture/homer-data-generator/internal/schema"
)

// Options controls parquet generation for hep_proto_1_call.
type Options struct {
	OutputDir     string // parquet data root (= DataPath when using DuckLake)
	CatalogPath   string // when set: write via DuckLake (ducklake-*.parquet + catalog.sqlite)
	LakeName      string
	Days          int
	TargetGB      float64
	RowsPerFile   int
	FilesPerDay   int
	PayloadBytes  int // 0 = auto from TargetGB
	StartDate     time.Time
	SeedCallID    string
	SeedCallRatio float64
	Table         string
	FlushEachBatch bool // CALL ducklake_flush_inlined_data after each insert batch
	CompactAtEnd   bool // run merge_adjacent_files when done
	MemoryLimit    string
	TempDirectory  string
	Threads        int
	ReopenEvery    int // close+reopen DuckDB every N batches (0 = once per day)
}

// Result summarizes a generate run.
type Result struct {
	FilesWritten int
	RowsWritten  int64
	BytesWritten int64
	OutputDir    string
	CatalogPath  string
}

// Generate writes hep_proto_1_call data.
//
// With CatalogPath set (recommended for Homer): INSERT INTO DuckLake table —
// files are main/hep_proto_1_call/date=YYYY-MM-DD/ducklake-{uuid}.parquet and
// catalog.sqlite is updated automatically. No separate register step.
//
// Without CatalogPath: legacy raw COPY to data_NNNNN.parquet (use register after).
func Generate(opts Options) (Result, error) {
	opts.applyDefaults()
	if err := opts.validate(); err != nil {
		return Result{}, err
	}

	payloadBytes := opts.PayloadBytes
	if payloadBytes <= 0 {
		payloadBytes = opts.autoPayloadBytes()
	}

	if opts.CatalogPath != "" {
		return generateDuckLake(opts, payloadBytes)
	}
	return generateRawParquet(opts, payloadBytes)
}

func generateDuckLake(opts Options, payloadBytes int) (Result, error) {
	lake, err := openLake(LakeConfig{
		CatalogPath:   opts.CatalogPath,
		DataPath:      opts.OutputDir,
		LakeName:      opts.LakeName,
		Table:         opts.Table,
		MemoryLimit:   opts.MemoryLimit,
		TempDirectory: opts.TempDirectory,
		Threads:       opts.Threads,
	})
	if err != nil {
		return Result{}, err
	}
	defer lake.Close()

	staging := "staging_hep_proto_1_call"
	if err := lake.ensureStaging(staging); err != nil {
		return Result{}, fmt.Errorf("create staging: %w", err)
	}

	br := &batchRunner{
		db:           lake.db,
		lake:         lake,
		opts:         opts,
		payloadBytes: payloadBytes,
		staging:      staging,
		ducklakeMode: true,
	}
	return br.run()
}

func generateRawParquet(opts Options, payloadBytes int) (Result, error) {
	if err := os.MkdirAll(filepath.Join(opts.OutputDir, "main", opts.Table), 0o755); err != nil {
		return Result{}, err
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return Result{}, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	for _, stmt := range []string{
		"SET threads TO 4",
		"SET preserve_insertion_order = false",
	} {
		if _, err := db.Exec(stmt); err != nil {
			return Result{}, err
		}
	}

	staging := "staging_hep_proto_1_call"
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (%s)", staging, schema.CallCreateSQL)); err != nil {
		return Result{}, fmt.Errorf("create staging: %w", err)
	}
	defer func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + staging) }()

	fmt.Println("WARNING: raw parquet mode — files are data_NNNNN.parquet, not ducklake-{uuid}.")
	fmt.Println("         For Homer use: generate --catalog ... --data-path ...")

	br := &batchRunner{
		db:           db,
		opts:         opts,
		payloadBytes: payloadBytes,
		staging:      staging,
	}
	return br.run()
}

type batchRunner struct {
	db           *sql.DB
	lake         *Lake
	opts         Options
	payloadBytes int
	staging      string
	ducklakeMode bool
}

func (br *batchRunner) reopenIfNeeded(fileIdx, totalFiles int) error {
	if br.lake == nil {
		return nil
	}
	every := br.opts.ReopenEvery
	if every <= 0 {
		every = br.opts.FilesPerDay
	}
	if fileIdx%every != 0 || fileIdx >= totalFiles {
		return nil
	}
	fmt.Printf("  reopening DuckDB session after batch %d/%d (release memory)...\n", fileIdx, totalFiles)
	if err := br.lake.reopen(); err != nil {
		return err
	}
	br.db = br.lake.db
	return br.lake.ensureStaging(br.staging)
}

func (br *batchRunner) run() (Result, error) {
	methods := []string{"INVITE", "ACK", "BYE", "CANCEL", "PRACK", "UPDATE", "INFO"}
	respCodes := []string{"", "", "", "100", "180", "200", "486", "487"}

	totalFiles := br.opts.Days * br.opts.FilesPerDay
	var res Result
	res.OutputDir = br.opts.OutputDir
	res.CatalogPath = br.opts.CatalogPath

	mode := "raw parquet"
	if br.ducklakeMode {
		mode = "DuckLake (catalog + ducklake-*.parquet)"
	}
	fmt.Printf("Generating ~%.1f GiB of %s over %d days [%s]\n",
		br.opts.TargetGB, br.opts.Table, br.opts.Days, mode)
	fmt.Printf("Data path: %s\n", br.opts.OutputDir)
	if br.ducklakeMode {
		fmt.Printf("Catalog:   %s\n", br.opts.CatalogPath)
	}

	fileIdx := 0
	for day := 0; day < br.opts.Days; day++ {
		dateStr := br.opts.StartDate.AddDate(0, 0, day).Format("2006-01-02")
		partDir := filepath.Join(br.opts.OutputDir, "main", br.opts.Table, "date="+dateStr)
		if !br.ducklakeMode {
			if err := os.MkdirAll(partDir, 0o755); err != nil {
				return res, err
			}
		}

		for f := 0; f < br.opts.FilesPerDay; f++ {
			fileIdx++
			seedOffset := fileIdx * br.opts.RowsPerFile

			insertSQL := buildInsertSQL(br.staging, insertParams{
				rows:          br.opts.RowsPerFile,
				dateStr:       dateStr,
				dayOffset:     day,
				fileOffset:    seedOffset,
				payloadBytes:  br.payloadBytes,
				seedCallID:    br.opts.SeedCallID,
				seedCallRatio: br.opts.SeedCallRatio,
				methods:       methods,
				respCodes:     respCodes,
			})

			if _, err := br.db.Exec(fmt.Sprintf("DELETE FROM %s", br.staging)); err != nil {
				return res, fmt.Errorf("clear staging: %w", err)
			}
			if _, err := br.db.Exec(insertSQL); err != nil {
				return res, fmt.Errorf("fill staging day=%s batch=%d: %w", dateStr, f+1, err)
			}

			if br.ducklakeMode {
				n, err := br.lake.InsertStaging(br.staging)
				if err != nil {
					return res, fmt.Errorf("insert into ducklake day=%s batch=%d: %w", dateStr, f+1, err)
				}
				res.RowsWritten += n
				if br.opts.FlushEachBatch {
					if err := br.lake.FlushInlined(); err != nil {
						return res, fmt.Errorf("flush_inlined_data: %w", err)
					}
				}
			} else {
				outPath := filepath.Join(partDir, fmt.Sprintf("data_%05d.parquet", fileIdx))
				copySQL := fmt.Sprintf(
					`COPY (SELECT * FROM %s) TO '%s' (FORMAT PARQUET, COMPRESSION SNAPPY)`,
					br.staging, escapeSQLPath(outPath),
				)
				if _, err := br.db.Exec(copySQL); err != nil {
					return res, fmt.Errorf("copy parquet: %w", err)
				}
				fi, err := os.Stat(outPath)
				if err != nil {
					return res, err
				}
				res.FilesWritten++
				res.RowsWritten += int64(br.opts.RowsPerFile)
				res.BytesWritten += fi.Size()
			}

			if err := br.reopenIfNeeded(fileIdx, totalFiles); err != nil {
				return res, fmt.Errorf("reopen duckdb: %w", err)
			}

			if fileIdx%10 == 0 || fileIdx == totalFiles {
				pct := float64(fileIdx) / float64(totalFiles) * 100
				if br.ducklakeMode {
					files, bytes, _ := br.lake.ParquetStats()
					fmt.Printf("  [%5.1f%%] %d/%d batches  %d ducklake files  %.2f GiB\n",
						pct, fileIdx, totalFiles, files, float64(bytes)/(1<<30))
				} else {
					fmt.Printf("  [%5.1f%%] %d/%d files  %.2f GiB\n",
						pct, fileIdx, totalFiles, float64(res.BytesWritten)/(1<<30))
				}
			}
		}
	}

	if br.ducklakeMode {
		if !br.opts.FlushEachBatch {
			if err := br.lake.FlushInlined(); err != nil {
				return res, fmt.Errorf("final flush_inlined_data: %w", err)
			}
		}
		if br.opts.CompactAtEnd {
			fmt.Println("Running ducklake_merge_adjacent_files...")
			if err := br.lake.MergeAdjacent(100); err != nil {
				return res, fmt.Errorf("merge_adjacent_files: %w", err)
			}
		}
		files, bytes, err := br.lake.ParquetStats()
		if err != nil {
			return res, err
		}
		res.FilesWritten = files
		res.BytesWritten = bytes
		if res.RowsWritten == 0 {
			res.RowsWritten, _ = br.lake.RowCount()
		}
	}

	fmt.Printf("Done: %d parquet files, %d rows, %.2f GiB\n",
		res.FilesWritten, res.RowsWritten, float64(res.BytesWritten)/(1<<30))
	if br.ducklakeMode {
		fmt.Printf("Catalog updated: %s\n", br.opts.CatalogPath)
	}
	return res, nil
}

func (o *Options) applyDefaults() {
	if o.Table == "" {
		o.Table = schema.CallTable
	}
	if o.LakeName == "" {
		o.LakeName = "homer_lake"
	}
	if o.StartDate.IsZero() {
		o.StartDate = time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -o.Days)
	}
}

func (o Options) validate() error {
	if strings.TrimSpace(o.OutputDir) == "" {
		return fmt.Errorf("output/data-path is required")
	}
	if o.Days < 1 {
		return fmt.Errorf("days must be >= 1")
	}
	if o.TargetGB <= 0 {
		return fmt.Errorf("target-gb must be > 0")
	}
	if o.RowsPerFile < 100 {
		return fmt.Errorf("rows-per-file must be >= 100")
	}
	if o.FilesPerDay < 1 {
		return fmt.Errorf("files-per-day must be >= 1")
	}
	if o.Table != schema.CallTable {
		return fmt.Errorf("only %s is supported for now", schema.CallTable)
	}
	if o.SeedCallRatio < 0 || o.SeedCallRatio > 1 {
		return fmt.Errorf("seed-call-ratio must be 0..1")
	}
	return nil
}

func (o Options) autoPayloadBytes() int {
	totalRows := float64(o.Days * o.FilesPerDay * o.RowsPerFile)
	targetBytes := o.TargetGB * (1 << 30)
	const fixedBytes = 600
	avg := targetBytes / totalRows
	p := int(math.Round(avg - fixedBytes))
	if p < 256 {
		p = 256
	}
	return p
}

type insertParams struct {
	rows          int
	dateStr       string
	dayOffset     int
	fileOffset    int
	payloadBytes  int
	seedCallID    string
	seedCallRatio float64
	methods       []string
	respCodes     []string
}

func buildInsertSQL(staging string, p insertParams) string {
	methodList := quoteList(p.methods)
	respList := quoteList(p.respCodes)
	methodN := len(p.methods)
	respN := len(p.respCodes)
	seed := escapeSQLString(p.seedCallID)
	payloadLen := p.payloadBytes

	return fmt.Sprintf(`
INSERT INTO %s
SELECT
	uuid()::VARCHAR AS uuid,
	'%s'::DATE AS date,
	('%s 00:00:00'::TIMESTAMP
		+ (i %% 86400) * INTERVAL '1 second'
		+ (i %% 1000) * INTERVAL '1 millisecond') AS timestamp,
	CASE
		WHEN (i + %d) %% 1000 < CAST(%g * 1000 AS INTEGER)
		THEN '%s'
		ELSE 'gen-' || ((i + %d) %% 500000)::VARCHAR || '@lab.local'
	END AS session_id,
	'user' || ((i + %d) %% 10000)::VARCHAR AS caller,
	'peer' || ((i + %d) %% 10000)::VARCHAR AS callee,
	'10.' || ((i + %d) %% 255)::VARCHAR || '.' || (((i + %d) / 255) %% 255)::VARCHAR || '.' || ((i + %d) %% 254 + 1)::VARCHAR AS src_ip,
	'10.' || (((i + %d) / 1000) %% 255)::VARCHAR || '.' || (((i + %d) / 10000) %% 255)::VARCHAR || '.' || ((i + %d) %% 254 + 1)::VARCHAR AS dst_ip,
	(5060 + (i %% 100))::UINTEGER AS src_port,
	(5060 + ((i + 7) %% 100))::UINTEGER AS dst_port,
	list_extract(%s, 1 + (i %% %d)) AS method,
	list_extract(%s, 1 + (i %% %d)) AS response_code,
	list_extract(%s, 1 + (i %% %d)) AS cseq_method,
	17::UINTEGER AS protocol,
	'node-gen-1' AS node_id,
	CASE
		WHEN (i + %d) %% 1000 < CAST(%g * 1000 AS INTEGER)
		THEN '%s'
		ELSE 'cid-' || ((i + %d) %% 500000)::VARCHAR
	END AS cid,
	repeat('X', %d) AS payload,
	json_object(
		'via', 'SIP/2.0/UDP 10.0.0.1:5060',
		'user_agent', 'homer-data-generator/1.0',
		'file_offset', i + %d
	) AS data_extra
FROM generate_series(1, %d) AS t(i)`,
		staging,
		p.dateStr, p.dateStr,
		p.fileOffset, p.seedCallRatio, seed, p.fileOffset,
		p.fileOffset, p.fileOffset,
		p.fileOffset, p.fileOffset, p.fileOffset,
		p.fileOffset, p.fileOffset, p.fileOffset,
		methodList, methodN,
		respList, respN,
		methodList, methodN,
		p.fileOffset, p.seedCallRatio, seed, p.fileOffset,
		payloadLen, p.fileOffset,
		p.rows,
	)
}

func quoteList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = "'" + escapeSQLString(s) + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func escapeSQLPath(p string) string {
	return strings.ReplaceAll(p, "'", "''")
}
