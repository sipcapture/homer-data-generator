package gen

import "testing"

func TestAutoPayloadBytes_80GiBProfile(t *testing.T) {
	o := Options{Days: 14, FilesPerDay: 32, RowsPerFile: 25000, TargetGB: 80}
	got := o.autoPayloadBytes()
	if got < 6000 {
		t.Fatalf("payload bytes = %d, want ~7000 for 80 GiB / 11.2M rows", got)
	}
}

func TestPayloadColumnSQL_compressible(t *testing.T) {
	s := payloadColumnSQL(100, 500, true)
	if s != "repeat('X', 500) AS payload" {
		t.Fatalf("unexpected: %s", s)
	}
}

func TestPayloadColumnSQL_sized(t *testing.T) {
	s := payloadColumnSQL(100, 500, false)
	if s == "" || s == "repeat('X', 500) AS payload" {
		t.Fatalf("expected md5-based payload, got: %s", s)
	}
}
