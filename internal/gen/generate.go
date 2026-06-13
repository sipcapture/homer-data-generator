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
	OutputDir     string
	Days          int
	TargetGB      float64
	RowsPerFile   int
	FilesPerDay   int
	PayloadBytes  int // 0 = auto from TargetGB
	StartDate     time.Time
	SeedCallID    string
	SeedCallRatio float64 // fraction of rows using SeedCallID for session_id and cid
	Table         string
}

// Result summarizes a generate run.
type Result struct {
	FilesWritten int
	RowsWritten  int64
	BytesWritten int64
	OutputDir    string
}

// Generate writes hive-partitioned parquet under
// {OutputDir}/main/{table}/date=YYYY-MM-DD/*.parquet
func Generate(opts Options) (Result, error) {
	opts.applyDefaults()
	if err := opts.validate(); err != nil {
		return Result{}, err
	}

	payloadBytes := opts.PayloadBytes
	if payloadBytes <= 0 {
		payloadBytes = opts.autoPayloadBytes()
	}

	totalFiles := opts.Days * opts.FilesPerDay

	if err := os.MkdirAll(filepath.Join(opts.OutputDir, "main", opts.Table), 0o755); err != nil {
		return Result{}, err
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return Result{}, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if _, err := db.Exec("SET threads TO 4"); err != nil {
		return Result{}, fmt.Errorf("set threads: %w", err)
	}
	if _, err := db.Exec("SET preserve_insertion_order = false"); err != nil {
		return Result{}, fmt.Errorf("set preserve_insertion_order: %w", err)
	}

	staging := "staging_hep_proto_1_call"
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (%s)", staging, schema.CallCreateSQL)); err != nil {
		return Result{}, fmt.Errorf("create staging: %w", err)
	}
	defer func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + staging) }()

	methods := []string{"INVITE", "ACK", "BYE", "CANCEL", "PRACK", "UPDATE", "INFO"}
	respCodes := []string{"", "", "", "100", "180", "200", "486", "487"}

	var res Result
	res.OutputDir = opts.OutputDir
	fileIdx := 0

	fmt.Printf("Generating ~%.1f GiB of %s over %d days (%d rows/file, %d files/day, payload=%d B)\n",
		opts.TargetGB, opts.Table, opts.Days, opts.RowsPerFile, opts.FilesPerDay, payloadBytes)
	fmt.Printf("Output: %s\n", opts.OutputDir)

	for day := 0; day < opts.Days; day++ {
		partDate := opts.StartDate.AddDate(0, 0, day)
		dateStr := partDate.Format("2006-01-02")
		partDir := filepath.Join(opts.OutputDir, "main", opts.Table, "date="+dateStr)
		if err := os.MkdirAll(partDir, 0o755); err != nil {
			return res, err
		}

		for f := 0; f < opts.FilesPerDay; f++ {
			fileIdx++
			outPath := filepath.Join(partDir, fmt.Sprintf("data_%05d.parquet", fileIdx))
			seedOffset := fileIdx * opts.RowsPerFile

			insertSQL := buildInsertSQL(staging, insertParams{
				rows:          opts.RowsPerFile,
				dateStr:       dateStr,
				dayOffset:     day,
				fileOffset:    seedOffset,
				payloadBytes:  payloadBytes,
				seedCallID:    opts.SeedCallID,
				seedCallRatio: opts.SeedCallRatio,
				methods:       methods,
				respCodes:     respCodes,
			})

			if _, err := db.Exec(fmt.Sprintf("DELETE FROM %s", staging)); err != nil {
				return res, fmt.Errorf("clear staging: %w", err)
			}
			if _, err := db.Exec(insertSQL); err != nil {
				return res, fmt.Errorf("insert batch day=%s file=%d: %w", dateStr, f+1, err)
			}

			copySQL := fmt.Sprintf(
				`COPY (SELECT * FROM %s) TO '%s' (FORMAT PARQUET, COMPRESSION SNAPPY)`,
				staging, escapeSQLPath(outPath),
			)
			if _, err := db.Exec(copySQL); err != nil {
				return res, fmt.Errorf("copy parquet %s: %w", outPath, err)
			}

			fi, err := os.Stat(outPath)
			if err != nil {
				return res, err
			}

			res.FilesWritten++
			res.RowsWritten += int64(opts.RowsPerFile)
			res.BytesWritten += fi.Size()

			if fileIdx%10 == 0 || fileIdx == totalFiles {
				pct := float64(fileIdx) / float64(totalFiles) * 100
				fmt.Printf("  [%5.1f%%] %d/%d files  %.2f GiB written  last=%s\n",
					pct, fileIdx, totalFiles, float64(res.BytesWritten)/(1<<30), filepath.Base(outPath))
			}
		}
	}

	fmt.Printf("Done: %d files, %d rows, %.2f GiB\n",
		res.FilesWritten, res.RowsWritten, float64(res.BytesWritten)/(1<<30))
	return res, nil
}

func (o *Options) applyDefaults() {
	if o.Table == "" {
		o.Table = schema.CallTable
	}
	if o.StartDate.IsZero() {
		o.StartDate = time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -o.Days)
	}
}

func (o Options) validate() error {
	if strings.TrimSpace(o.OutputDir) == "" {
		return fmt.Errorf("output dir is required")
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
	// Fixed columns + JSON overhead ~ 600 bytes per row in parquet.
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

	// Spread timestamps within the partition day; vary session_id/cid for realism.
	// seedCallRatio fraction of rows use the fixed seed call-id (for LIKE search tests).
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
