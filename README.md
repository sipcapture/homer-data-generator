# homer-data-generator

Synthetic **DuckLake-compatible** parquet for [Homer](https://github.com/sipcapture/homer) load and OOM regression testing.

Generates `hep_proto_1_call` data in the same on-disk layout Homer uses:

```text
{output}/main/hep_proto_1_call/date=YYYY-MM-DD/data_00001.parquet
```

Default profile matches the long-range search OOM scenario: **~80 GiB over 14 days**, many small parquet files per day, plus a configurable fraction of rows with a fixed `Call-ID` for `LIKE` / `call_id` search tests.

## Requirements

- Go 1.22+
- CGO (DuckDB bindings, same stack as homer-core)
- DuckLake extensions if you use `register` (install once: `homer-core --install-extensions`)

## Quick start

### Smoke test (~50 MiB)

```bash
go run . generate --output ./parquet-smoke --target-gb 0.05 --days 2 --files-per-day 4
```

### Full OOM repro dataset (~80 GiB, 14 days)

```bash
go run . generate \
  --output /data/homer/parquet \
  --target-gb 80 \
  --days 14 \
  --rows-per-file 25000 \
  --files-per-day 32
```

Progress prints every 10 files. Payload column size is auto-tuned from `--target-gb` unless you set `--payload-bytes`.

### Seed Call-ID (search repro)

By default **0.1%** of rows use:

`9b9558fa657d11f1aba1000c29796214@91.102.10.105`

(same pattern as the production OOM log). Override:

```bash
--seed-call-id 'your-call-id@host' --seed-call-ratio 0.01
```

## Wire into Homer

### Option A — fresh local storage

1. Point Homer `storage.ducklake.data_path` at the generator output directory.
2. Start homer-core once so it creates `homer_catalog.sqlite` and table schemas.
3. Stop homer-core.
4. Run generate (or generate first, then start homer once for catalog only).
5. Import parquet into the catalog:

```bash
go run . register \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet
```

Or with homer-core:

```bash
homer-core ducklake compaction --compaction-recover -c /path/to/homer.json
```

6. Start homer-core and search in the UI: **call_id + 14 days**.

### Option B — append to existing lake

Stop homer-core, generate into the same `data_path`, then `register` or `--compaction-recover`.

## Commands

| Command | Purpose |
|---------|---------|
| `generate` | Write hive-partitioned parquet |
| `register` | `INSERT INTO homer_lake.main.hep_proto_1_call SELECT * FROM read_parquet(...)` |

### `generate` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--output` | `./parquet` | Data root |
| `--days` | `14` | Number of `date=` partitions |
| `--target-gb` | `80` | Target total size (approx) |
| `--rows-per-file` | `25000` | Rows per parquet file |
| `--files-per-day` | `32` | Files per day (small files ≈ post-flush production) |
| `--payload-bytes` | auto | Force SIP `payload` column size |
| `--start-date` | today−days | First partition `YYYY-MM-DD` |
| `--seed-call-id` | (see above) | Call-ID/CID for search repro rows |
| `--seed-call-ratio` | `0.001` | Fraction of rows with seed call-id |

### `register` flags

| Flag | Default |
|------|---------|
| `--catalog` | `/data/homer/homer_catalog.sqlite` |
| `--data-path` | `./parquet` |
| `--lake` | `homer_lake` |
| `--table` | `hep_proto_1_call` |

## Schema

Matches homer-core `hep_proto_1_call` ([STORAGE_LAYOUT.md](https://github.com/sipcapture/homer/blob/homer11/docs/STORAGE_LAYOUT.md)):

`uuid`, `date`, `timestamp`, `session_id`, `caller`, `callee`, `src_ip`, `dst_ip`, `src_port`, `dst_port`, `method`, `response_code`, `cseq_method`, `protocol`, `node_id`, `cid`, `payload`, `data_extra`

Parquet compression: **Snappy** (DuckDB default, same as Homer).

## License

AGPL-3.0-or-later (aligned with Homer ecosystem).
