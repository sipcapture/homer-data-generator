package gen

import "testing"

func TestAutoPayloadBytes_80GiBProfile_compressible(t *testing.T) {
	o := Options{
		Days: 14, FilesPerDay: 32, RowsPerFile: 25000, TargetGB: 80,
		CompressiblePayload: true,
	}
	got := o.autoPayloadBytes()
	// ~141k per-row md5-repeat → ~80 GiB Snappy parquet on disk
	if got < 120_000 || got > 170_000 {
		t.Fatalf("payload bytes = %d, want ~141000 for 80 GiB compressed on disk", got)
	}
}

func TestAutoPayloadBytes_tuneBatchSize(t *testing.T) {
	o := Options{
		Days: 14, FilesPerDay: 32, RowsPerFile: 25000, TargetGB: 80,
		CompressiblePayload: true,
	}
	p := o.autoPayloadBytes()
	before := o.Days * o.FilesPerDay * o.RowsPerFile
	o.tuneBatchSize(p, 256<<20)
	after := o.Days * o.FilesPerDay * o.RowsPerFile
	if o.RowsPerFile > 25000 {
		t.Fatalf("rows-per-file = %d, expected reduction for large payload", o.RowsPerFile)
	}
	if after < before-50000 || after > before+50000 {
		t.Fatalf("row count drift: before=%d after=%d", before, after)
	}
}

func TestPayloadColumnSQL_default(t *testing.T) {
	s := payloadColumnSQL(100, 500, Options{CompressiblePayload: true})
	if s == "" || s == "repeat('X', 500) AS payload" {
		t.Fatalf("expected per-row md5 payload, got: %s", s)
	}
}

func TestEstimateDiskSize_80GiB(t *testing.T) {
	o := Options{
		Days: 14, FilesPerDay: 32, RowsPerFile: 25000, TargetGB: 80,
		CompressiblePayload: true,
	}
	p := o.autoPayloadBytes()
	rows := float64(o.Days * o.FilesPerDay * o.RowsPerFile)
	est := rows * (compressedRowOverheadBytes + float64(p)*snappyPayloadDiskRatio)
	want := 80.0 * (1 << 30)
	if est < want*0.85 || est > want*1.15 {
		t.Fatalf("estimated disk %0.2f GiB, want ~80 GiB (payload=%d)", est/(1<<30), p)
	}
}
