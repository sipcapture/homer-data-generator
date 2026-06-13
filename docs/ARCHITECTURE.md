# Architecture

`homer-data-generator` produces synthetic SIP call data in the **same on-disk format** that [Homer](https://github.com/sipcapture/homer) uses via DuckLake. The primary goal is local regression testing — especially long-range searches and compaction OOM scenarios — without relying on production traffic or manual QA.

## Position in the Homer stack

```text
┌─────────────────────────────────────────────────────────────────────────┐
│  homer-data-generator (offline CLI)                                     │
│  init-catalog → generate → [compact]                                    │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │ writes
                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  Local filesystem                                                       │
│  homer_catalog.sqlite          ← DuckLake metadata (snapshots, files)   │
│  parquet/main/hep_proto_1_call/date=YYYY-MM-DD/ducklake-{uuid}.parquet  │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │ read at runtime
                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  homer-core                                                             │
│  writer (ingest) ──► DuckLake ──► node (search) ──► coordinator (UI/API)│
│  CompactionService: flush_inlined_data, merge_adjacent_files            │
└─────────────────────────────────────────────────────────────────────────┘
```

Homer does **not** read parquet by scanning directories alone. Every query goes through DuckLake, which uses `catalog.sqlite` to know which parquet files exist, their row counts, column statistics, and snapshot lineage. The generator therefore has two conceptual layers:

| Layer | What it is | Required for Homer search? |
|-------|------------|----------------------------|
| **Data** | Hive-partitioned parquet under `data_path/main/…` | Yes |
| **Catalog** | SQLite metadata at `catalog_path` | Yes |

In **`--catalog` mode** (recommended), both layers are written in one pass. In legacy raw mode, only parquet is written and a separate `register` step is needed.

## Repository layout

```text
homer-data-generator/
├── main.go                 # CLI: -v, init-catalog, generate, compact, register
├── version.go              # VERSION_APPLICATION, build metadata (ldflags)
├── internal/
│   ├── schema/
│   │   └── call.go         # hep_proto_1_call column DDL (mirrors homer-core)
│   └── gen/
│       ├── ducklake.go     # ATTACH lake, ensure table, insert, flush, merge
│       ├── generate.go     # batch loop + row synthesis (DuckDB SQL)
│       ├── compact.go      # maintenance wrapper
│       └── register.go     # legacy import for raw parquet only
└── docs/
    ├── ARCHITECTURE.md
    └── HOWTO.md
```

## CLI commands

```text
init-catalog
    │
    ├─► CREATE catalog.sqlite (via DuckLake ATTACH)
    ├─► CREATE TABLE homer_lake.main.hep_proto_1_call
    └─► ALTER … SET PARTITIONED BY (date), SORTED BY (timestamp)

generate [--catalog PATH]
    │
    ├─► [DuckLake mode] INSERT INTO lake.main.hep_proto_1_call
    │       └─► ducklake_flush_inlined_data per batch
    │
    └─► [Raw mode] COPY staging → data_NNNNN.parquet (legacy)

compact
    ├─► ducklake_flush_inlined_data
    └─► ducklake_merge_adjacent_files (max_compacted_files)

register  (legacy only)
    └─► INSERT INTO lake FROM read_parquet(raw files)
```

## Generate pipeline (DuckLake mode)

Each **batch** corresponds to one future small parquet file (similar to a Homer writer flush):

```text
  generate_series + random SIP fields
           │
           ▼
  TEMP TABLE staging_hep_proto_1_call
           │
           ▼
  INSERT INTO homer_lake.main.hep_proto_1_call SELECT * FROM staging
           │                    │
           │                    ├──► catalog.sqlite (ducklake_data_file, snapshots, stats)
           │                    └──► parquet/main/hep_proto_1_call/date=…/ducklake-{uuid}.parquet
           ▼
  CALL ducklake_flush_inlined_data('homer_lake')
```

### Why `ducklake-{uuid}.parquet`?

Homer’s writer does not name files manually. DuckLake assigns UUID-based filenames when persisting table data. Files like:

```text
ducklake-019ebb60-66b6-7ef9-8d4a-2c345b02eab5.parquet
```

are what you see after flush or compaction in production. The generator reproduces this by inserting through DuckLake rather than writing parquet with `COPY` directly.

### Partitioning

Table `hep_proto_1_call` is partitioned by **`date`** (Hive layout):

```text
parquet/main/hep_proto_1_call/date=2026-06-01/ducklake-….parquet
parquet/main/hep_proto_1_call/date=2026-06-02/ducklake-….parquet
…
```

**Default date window** (`--days N`, no `--start-date`):

```text
start_date = today_utc_midnight − N days
partitions = start_date, start_date+1, …, start_date+(N−1)
```

So with `--days 14` on `2026-06-13` UTC, partitions are `2026-05-30` … `2026-06-12` — **today is not included**. Set `--start-date YYYY-MM-DD` to fix the first partition explicitly.

Homer’s node rewrites searches into `UNION ALL` across lake parquet **and** in-memory buffer tables (`mem_hep_proto_1_call_a/b`). With homer-core stopped during generation, only lake files exist — which is fine for cold-storage load tests.

## Row synthesis

Rows are built entirely in **DuckDB SQL** (`generate_series` + expressions) for speed and schema fidelity:

| Column | Source |
|--------|--------|
| `uuid` | `uuid()` |
| `date` | partition day (`YYYY-MM-DD`) |
| `timestamp` | spread within the day (second + ms offset) |
| `session_id`, `cid` | mostly synthetic `gen-N@lab.local`; `--seed-call-ratio` rows use `--seed-call-id` |
| `caller`, `callee` | `userN` / `peerN` |
| `src_ip`, `dst_ip` | pseudo-random `10.x.x.x` |
| `method`, `response_code`, `cseq_method` | rotated SIP methods / responses |
| `payload` | md5 chunk string (`--target-gb` sized); `--compressible-payload` uses `repeat('X')` (Snappy shrinks to ~2 GiB for 80G target) |
| `data_extra` | JSON metadata |

Default **seed Call-ID** (`9b9558fa657d11f1aba1000c29796214@91.102.10.105`) matches the production OOM repro pattern (14-day range + `LIKE '%call_id%'`).

## Size model

Total volume is approximate:

```text
total_rows = days × files_per_day × rows_per_file
payload_bytes ≈ (target_gb × 2³⁰) / total_rows − fixed_overhead
```

Defaults (`14` days, `32` files/day, `25000` rows/file, `80` GiB) yield many small files — similar to a busy node before compaction — which stress DuckDB memory during merge and long-range scan.

## DuckDB dependencies

The tool uses `github.com/duckdb/duckdb-go/v2` (same family as homer-core). Required extensions:

- `ducklake` — lake attach, insert, maintenance procedures
- `sqlite` — catalog backend

Install once on the host:

```bash
homer-core --install-extensions
```

Runtime settings applied on open:

- `threads = 4` (override: `--threads`)
- `memory_limit = 8GB` (override: `--memory-limit`) — without this DuckDB claims ~80% of host RAM
- `temp_directory = <catalog_dir>/.duckdb_spill` (override: `--temp-directory`) — disk spill when batches exceed memory_limit
- `preserve_insertion_order = false` (lower peak memory during large inserts)
- Periodic DuckDB reconnect (default: once per day / `--files-per-day`) + sqlite GC of empty `ducklake_inlined_data_*` tables to cap RSS on long runs

## Modes compared

| Aspect | DuckLake (`--catalog`) | Raw (no `--catalog`) |
|--------|------------------------|----------------------|
| Parquet names | `ducklake-{uuid}.parquet` | `data_00001.parquet` |
| Catalog updated | Yes, each batch | No |
| Homer-ready | Yes, after `generate` | Needs `register` or `--compaction-recover` |
| Use case | Production-like testing | Quick parquet-only experiments |

## Schema contract

`internal/schema/call.go` mirrors homer-core `GetTableSchemas()` for `{ProtoTypeSIP, SIPTypeCall}` → `hep_proto_1_call`. Any schema change in Homer must be reflected here for generated data to remain queryable.

Reference: [Homer STORAGE_LAYOUT.md](https://github.com/sipcapture/homer/blob/homer11/docs/STORAGE_LAYOUT.md)

## Safety and concurrency

- **Stop homer-core** while running `generate`, `init-catalog`, `compact`, or `register` on the same `catalog_path`. DuckLake uses SQLite for the catalog; concurrent writers cause `database is locked`.
- Generation is **idempotent only on empty catalog**. Re-running `generate` on an existing populated lake **appends** data (new snapshots/files).
- For a clean slate, use a fresh directory pair or delete `catalog.sqlite` and `parquet/` before `init-catalog`.

## Related Homer components

| Homer module | Relevance |
|--------------|-----------|
| `src/storage/ducklake/tables.go` | Canonical schema, partitioning, flush |
| `src/writer/compaction.go` | `merge_adjacent_files`, `flush_inlined_data` — mirrored by `compact` |
| `src/node/node.go` | Search SQL rewrite, `UNION ALL` with mem buffers |
| `src/cli/system_cmd.go` | `recoverCatalog` — alternative to `register` |

## License

AGPL-3.0-or-later — aligned with Homer ecosystem.
