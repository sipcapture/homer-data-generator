# homer-data-generator

Synthetic **DuckLake** data for [Homer](https://github.com/sipcapture/homer) load and OOM regression testing.

Writes real DuckLake artifacts — the same layout Homer produces after flush/compaction:

```text
/data/homer/
├── homer_catalog.sqlite
└── parquet/main/hep_proto_1_call/
    └── date=2026-06-12/
        └── ducklake-019ebb60-66b6-7ef9-8d4a-2c345b02eab5.parquet
```

**`--catalog` mode** (recommended): `INSERT INTO` DuckLake → `ducklake-{uuid}.parquet` on disk **and** rows registered in `catalog.sqlite` automatically. No separate `register` step.

**Docs:** [Architecture](docs/ARCHITECTURE.md) · [HOWTO](docs/HOWTO.md)

```bash
make build          # bin/homer-data-generator
make version        # or: ./bin/homer-data-generator -v
make smoke          # quick ~50 MiB test in /tmp
make help           # all targets
```

## Requirements

- Go 1.22+, CGO
- DuckLake extensions: `homer-core --install-extensions` (once)
- `make` (optional — `make help`)

## Quick start (Homer-compatible)

```bash
make build

# 1. Create empty catalog + table schema
./bin/homer-data-generator init-catalog \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet

# 2. Generate ~80 GiB / 14 days (stop homer-core while running)
#    Default dates: (today UTC − 14 days) … (yesterday UTC) — today is NOT included.
./bin/homer-data-generator generate \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet \
  --target-gb 80 \
  --days 14

# 3. Optional: merge small files (like CompactionService)
./bin/homer-data-generator compact \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet

# 4. Point homer.json at the same paths and start homer-core
```

Smoke test (~50 MiB):

```bash
make build
./bin/homer-data-generator generate \
  --catalog /tmp/homer_catalog.sqlite \
  --data-path /tmp/parquet \
  --target-gb 0.05 --days 2 --files-per-day 4
```

## Homer config

```json
"storage": {
  "ducklake": {
    "catalog_path": "/data/homer/homer_catalog.sqlite",
    "data_path": "/data/homer/parquet"
  }
}
```

Search repro: default **0.1%** of rows use Call-ID  
`9b9558fa657d11f1aba1000c29796214@91.102.10.105` (override with `--seed-call-id`).

## Commands

| Command | Purpose |
|---------|---------|
| `-v`, `--version` | Print version, build info, and exit |
| `init-catalog` | Create `catalog.sqlite` + `hep_proto_1_call` (partitioned by `date`) |
| `generate --catalog …` | Insert batches → `ducklake-*.parquet` + catalog |
| `compact` | `flush_inlined_data` + `merge_adjacent_files` |
| `register` | **Legacy only** — import raw `data_NNNNN.parquet` (generate without `--catalog`) |

### `generate` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--catalog` | — | **Required for Homer.** DuckLake sqlite path |
| `--data-path` | `/data/homer/parquet` | Parquet root |
| `--target-gb` | `80` | Approximate **parquet on disk** size (Snappy) |
| `--compressible-payload` | `false` | Fast `repeat('X')` — **do not use** if you need real `--target-gb` volume |
| `--days` | `14` | Number of `date=` partitions (see below) |
| `--start-date` | auto | First partition `YYYY-MM-DD` (default: today UTC − `--days`) |
| `--rows-per-file` | `25000` | Rows per insert batch |
| `--files-per-day` | `32` | Batches per day |
| `--compact` | `false` | Run merge after generate |
| `--seed-call-id` | (OOM repro ID) | Fixed Call-ID for search tests |

### Date partitions (`--days`)

All dates are **UTC** calendar days. With `--days 14` and no `--start-date`:

- **start** = today UTC (midnight) minus 14 days  
- **partitions** = `start`, `start+1`, …, `start+13` (14 days total)  
- **today is excluded** — the last partition is yesterday

Example: if today is `2026-06-13`, partitions are `2026-05-30` … `2026-06-12`.

To include today in a 14-day window:

```bash
./bin/homer-data-generator generate ... --days 14 --start-date $(date -u -d '13 days ago' +%Y-%m-%d)
```

## Do I need `register`?

| Generate mode | Catalog | Filenames | `register` needed? |
|---------------|---------|-----------|-------------------|
| `--catalog` set | updated automatically | `ducklake-{uuid}.parquet` | **No** |
| without `--catalog` | not touched | `data_00001.parquet` | Yes (`register` or `homer-core --compaction-recover`) |

## Schema

Matches homer-core `hep_proto_1_call`:  
`uuid`, `date`, `timestamp`, `session_id`, `caller`, `callee`, `src_ip`, `dst_ip`, `src_port`, `dst_port`, `method`, `response_code`, `cseq_method`, `protocol`, `node_id`, `cid`, `payload`, `data_extra`

## License

AGPL-3.0-or-later
