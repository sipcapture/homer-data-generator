# HOWTO

Step-by-step recipes for common tasks with `homer-data-generator`.

See also: [ARCHITECTURE.md](ARCHITECTURE.md)

## Prerequisites

```bash
git clone https://github.com/sipcapture/homer-data-generator
cd homer-data-generator

# DuckDB + DuckLake extensions (once per machine)
homer-core --install-extensions

# Build optional
go build -o homer-data-generator .
```

You need **Go 1.22+** and **CGO** (DuckDB bindings).

Disk: plan at least **`target-gb + 20%`** free space under `--data-path` (default profile ≈ **80 GiB**).

---

## 1. Smoke test (~50 MiB, 2 minutes)

Verifies extensions, catalog, and `ducklake-*.parquet` output:

```bash
go run . init-catalog \
  --catalog /tmp/homer_catalog.sqlite \
  --data-path /tmp/parquet

go run . generate \
  --catalog /tmp/homer_catalog.sqlite \
  --data-path /tmp/parquet \
  --target-gb 0.05 \
  --days 2 \
  --files-per-day 4 \
  --rows-per-file 1000
```

Check output:

```bash
find /tmp/parquet -name 'ducklake-*.parquet' | head
sqlite3 /tmp/homer_catalog.sqlite \
  "SELECT count(*) FROM __ducklake_metadata_homer_lake.ducklake_data_file"
```

---

## 2. Full OOM repro dataset (~80 GiB, 14 days)

Matches the long-range `call_id` search scenario that triggered DuckDB OOM on a 2 GiB memory limit.

```bash
DATA=/data/homer
CATALOG=$DATA/homer_catalog.sqlite
PARQUET=$DATA/parquet

# Stop homer-core if it uses the same paths
sudo systemctl stop homer-core   # or docker compose stop homer

go run . init-catalog \
  --catalog "$CATALOG" \
  --data-path "$PARQUET"

go run . generate \
  --catalog "$CATALOG" \
  --data-path "$PARQUET" \
  --target-gb 80 \
  --days 14 \
  --rows-per-file 25000 \
  --files-per-day 32
```

Expected: **448** insert batches (14 × 32), hundreds of `ducklake-*.parquet` files, catalog row count ≈ 11.2M rows.

---

## 3. Wire into Homer

`homer.json` (or docker env):

```json
{
  "storage": {
    "ducklake": {
      "catalog_path": "/data/homer/homer_catalog.sqlite",
      "data_path": "/data/homer/parquet",
      "tuning": {
        "memory_limit": "2GB",
        "temp_directory": "/data/homer/.duckdb_spill"
      }
    }
  }
}
```

Start homer-core:

```bash
sudo systemctl start homer-core
# or: docker compose up -d homer
```

### UI search repro

1. Open Homer dashboard.
2. Time range: **14 days** (covering generated `date=` partitions).
3. Search field **Call-ID**: `9b9558fa657d11f1aba1000c29796214@91.102.10.105`  
   (default seed ID — 0.1% of rows match).
4. Watch homer-core logs for query duration / OOM.

### API repro

```bash
curl -s -b cookies.txt -X POST http://localhost:8080/api/v4/transactions/search \
  -H 'Content-Type: application/json' \
  -d '{
    "search": {"call_id": "9b9558fa657d11f1aba1000c29796214@91.102.10.105"},
    "timestamp": {"from": 1780918439651, "to": 1781004839651}
  }'
```

Adjust `from`/`to` to match your `--start-date` and `--days`.

---

## 4. Run compaction (like CompactionService)

After generate, optionally merge small adjacent files:

```bash
go run . compact \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet \
  --max-compacted-files 100
```

Or compact at end of generate:

```bash
go run . generate ... --compact
```

Use this to test compaction OOM separately from search OOM.

---

## 5. Custom Call-ID density

More seed rows → heavier `LIKE '%call_id%'` scans:

```bash
go run . generate \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path /data/homer/parquet \
  --target-gb 10 --days 14 \
  --seed-call-id 'my-test-call@10.0.0.1' \
  --seed-call-ratio 0.05
```

`--seed-call-ratio 0.05` = 5% of rows share that Call-ID.

---

## 6. Append to an existing Homer lake

**Warning:** this adds snapshots; it does not replace data.

1. Stop homer-core.
2. Use the **same** `--catalog` and `--data-path` as running config.
3. Run `generate` (skip `init-catalog` if table already exists).

To start fresh: backup then remove `homer_catalog.sqlite` and `parquet/`, then `init-catalog` + `generate`.

---

## 7. Legacy raw parquet + register

Only if you generated **without** `--catalog`:

```bash
# Generates data_00001.parquet — NOT Homer-ready alone
go run . generate --output ./parquet --target-gb 1 --days 3

# Import into catalog (homer-core stopped)
go run . register \
  --catalog /data/homer/homer_catalog.sqlite \
  --data-path ./parquet
```

Equivalent Homer command:

```bash
homer-core ducklake compaction --compaction-recover -c homer.json
```

---

## 8. Troubleshooting

| Symptom | Fix |
|---------|-----|
| `failed to load ducklake extension` | Run `homer-core --install-extensions` |
| `database is locked` | Stop homer-core; only one process per `catalog_path` |
| Search returns 0 rows | Check `date` range covers `--start-date` … `--start-date + days` |
| Search returns 0 for seed Call-ID | Increase `--seed-call-ratio` or verify `--seed-call-id` |
| `No files found` in UI but parquet exists | You used raw mode without `register` — use `--catalog` or `register` |
| OOM during **generate** | Lower `--target-gb`, `--rows-per-file`, or `--files-per-day`; ensure free RAM |
| OOM during **Homer search** | Tune `storage.ducklake.tuning` (`memory_limit`, `temp_directory`) in homer.json |

### Verify catalog vs disk

```bash
# Files registered in catalog
sqlite3 /data/homer/homer_catalog.sqlite <<'SQL'
SELECT t.table_name, count(*) AS files, sum(f.record_count) AS rows
FROM __ducklake_metadata_homer_lake.ducklake_data_file f
JOIN __ducklake_metadata_homer_lake.ducklake_table t ON t.table_id = f.table_id
GROUP BY 1;
SQL

# Files on disk
find /data/homer/parquet/main/hep_proto_1_call -name 'ducklake-*.parquet' | wc -l
du -sh /data/homer/parquet
```

### Homer SQL CLI spot-check

```bash
homer-core sql -c homer.json
```

```sql
SELECT count(*) FROM homer_lake.main.hep_proto_1_call;
SELECT count(*) FROM homer_lake.main.hep_proto_1_call
  WHERE session_id LIKE '%9b9558fa657d11f1aba1000c29796214%';
```

---

## 9. Docker-friendly paths

Example bind mounts:

```yaml
volumes:
  - ./homer-data:/data/homer
```

Inside container:

```text
/data/homer/homer_catalog.sqlite
/data/homer/parquet/
```

Run generator on the **host** (or a one-off container with the same mounts) while the main homer container is stopped.

---

## Quick reference

```bash
# Setup
go run . init-catalog --catalog PATH --data-path PATH

# Generate (Homer-ready)
go run . generate --catalog PATH --data-path PATH --target-gb 80 --days 14

# Compaction
go run . compact --catalog PATH --data-path PATH

# Help
go run . help
```
