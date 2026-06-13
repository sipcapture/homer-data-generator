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

## Requirements

- Go 1.22+, CGO
- DuckLake extensions: `homer-core --install-extensions` (once)

## Quick start (Homer-compatible)

```bash
# 1. Create empty catalog + table schema
go run . init-catalog \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet

# 2. Generate ~80 GiB / 14 days (stop homer-core while running)
go run . generate \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet \
  --target-gb 80 \
  --days 14

# 3. Optional: merge small files (like CompactionService)
go run . compact \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet

# 4. Point homer.json at the same paths and start homer-core
```

Smoke test (~50 MiB):

```bash
go run . generate \
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
| `init-catalog` | Create `catalog.sqlite` + `hep_proto_1_call` (partitioned by `date`) |
| `generate --catalog …` | Insert batches → `ducklake-*.parquet` + catalog |
| `compact` | `flush_inlined_data` + `merge_adjacent_files` |
| `register` | **Legacy only** — import raw `data_NNNNN.parquet` (generate without `--catalog`) |

### `generate` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--catalog` | — | **Required for Homer.** DuckLake sqlite path |
| `--data-path` | `/data/homer/parquet` | Parquet root |
| `--target-gb` | `80` | Approximate total size |
| `--days` | `14` | `date=` partitions |
| `--rows-per-file` | `25000` | Rows per insert batch |
| `--files-per-day` | `32` | Batches per day |
| `--compact` | `false` | Run merge after generate |
| `--seed-call-id` | (OOM repro ID) | Fixed Call-ID for search tests |

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
