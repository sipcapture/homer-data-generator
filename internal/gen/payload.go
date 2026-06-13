package gen

import (
	"fmt"
	"math"
)

// Parquet Snappy calibration for the default payload (per-row md5, repeated to length).
// Measured on DuckLake flush: 20052-byte payload → ~1.0 KiB/row on disk (~0.05 disk/raw).
const (
	snappyPayloadDiskRatio     = 0.05
	compressedRowOverheadBytes = 50.0

	// repeat('X') compresses far more (~52:1); only for --repeat-x-payload smoke runs.
	snappyDiskBytesPerRepeatXByte = 0.0187
)

// autoPayloadBytes returns VARCHAR payload length for one row.
// --target-gb is always Snappy parquet size on disk (GiB).
func (o Options) autoPayloadBytes() int {
	totalRows := float64(o.Days * o.FilesPerDay * o.RowsPerFile)
	if totalRows <= 0 {
		return 256
	}
	targetBytes := o.TargetGB * (1 << 30)

	var ratio float64
	switch {
	case !o.CompressiblePayload:
		ratio = 1.0 // string_agg md5 chunks ≈ on-disk size
	case o.RepeatXPayload:
		ratio = snappyDiskBytesPerRepeatXByte
	default:
		ratio = snappyPayloadDiskRatio
	}

	raw := (targetBytes - totalRows*compressedRowOverheadBytes) / (totalRows * ratio)
	p := int(math.Round(raw))
	if p < 256 {
		p = 256
	}
	return p
}

// tuneBatchSize lowers rows-per-file (and raises files-per-day) so a single
// INSERT batch stays within maxStagingBytes.
func (o *Options) tuneBatchSize(payloadBytes int, maxStagingBytes int64) {
	if payloadBytes <= 0 || maxStagingBytes <= 0 {
		return
	}
	maxRows := int(maxStagingBytes / int64(payloadBytes))
	if maxRows < 100 {
		maxRows = 100
	}
	if o.RowsPerFile <= maxRows {
		return
	}
	totalRows := o.Days * o.FilesPerDay * o.RowsPerFile
	o.RowsPerFile = maxRows
	perDay := int(math.Ceil(float64(totalRows) / float64(o.Days*o.RowsPerFile)))
	if perDay < 1 {
		perDay = 1
	}
	o.FilesPerDay = perDay
}

func payloadModeLabel(o Options) string {
	switch {
	case !o.CompressiblePayload:
		return "incompressible md5 chunks"
	case o.RepeatXPayload:
		return "repeat('X') — very small on disk; not for --target-gb volume tests"
	default:
		return "per-row md5 (Snappy-compressible), --target-gb = parquet on disk"
	}
}

// payloadColumnSQL builds the payload column expression.
func payloadColumnSQL(fileOffset, payloadLen int, o Options) string {
	if o.RepeatXPayload && o.CompressiblePayload {
		return fmt.Sprintf("repeat('X', %d) AS payload", payloadLen)
	}
	if !o.CompressiblePayload {
		if payloadLen <= 0 {
			return "''::VARCHAR AS payload"
		}
		chunks := (payloadLen / 32) + 1
		return fmt.Sprintf(
			`substr((SELECT string_agg(md5((i + %d + s.v)::VARCHAR), '') FROM generate_series(0, %d) AS s(v)), 1, %d) AS payload`,
			fileOffset, chunks, payloadLen,
		)
	}
	if payloadLen <= 0 {
		return "''::VARCHAR AS payload"
	}
	chunks := (payloadLen / 32) + 2
	return fmt.Sprintf(
		`substr(repeat(md5((i + %d)::VARCHAR), %d), 1, %d) AS payload`,
		fileOffset, chunks, payloadLen,
	)
}
