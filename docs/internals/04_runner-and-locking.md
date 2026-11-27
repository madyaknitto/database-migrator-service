# Execution Runner & Advisory Locking

Paket [`internal/runner`](../../internal/runner/runner.go) mengorkestrasi eksekusi migrasi (`Up`, `Status`, `Baseline`) di atas interface `Store` yang diimplementasikan untuk PostgreSQL oleh [`PostgresStore`](../../internal/runner/postgres_store.go).

## Abstraksi `Store` & Koneksi Tunggal `PostgresStore`

```go
type Store interface {
	TryAdvisoryLock(ctx context.Context, lockKey int64) (bool, error)
	UnlockAdvisory(ctx context.Context, lockKey int64) error
	EnsureTrackingTable(ctx context.Context, schema, table string) error
	ListApplied(ctx context.Context, schema, table string) ([]AppliedRecord, error)
	ExecInTx(ctx context.Context, lockTimeout, stmtTimeout, sql string, record AppliedRecord, schema, table string) error
	ExecNoTx(ctx context.Context, lockTimeout, stmtTimeout string, statements []string, record AppliedRecord, schema, table string) error
	InsertRecord(ctx context.Context, schema, table string, record AppliedRecord) error
	Close(ctx context.Context) error
}
```

`ConnectPostgres` membuka satu `*pgx.Conn` per target database (bukan pool). Penggunaan koneksi tunggal sangat penting karena session advisory lock PostgreSQL (`pg_try_advisory_lock`) dan pengaturan `SET SESSION` di mode `no-tx` terikat pada session koneksi tertentu.

## Advisory Lock (`LockKey` & `AcquireLock`)

- **`LockKey(dbName)`**: Menghasilkan `int64` deterministik via FNV-64a dari `"database_migrator_service:" + seed`. Fungsi `lockKeySeed` memprioritaskan `ActualDBName` (hasil resolusi `database_env`) agar jika 2 environment memakai DB berbeda atau nama logis berbeda menunjuk ke DB aktual yang sama, lock tetap akurat.
- **`AcquireLock`**:
  - Melakukan polling `SELECT pg_try_advisory_lock($1)` setiap `PollInterval` (default `500ms`) hingga `LockWaitTimeout` (default `120s`).
  - Mencetak log progres ke `opts.Logf` setiap 5 detik (`lockLogInterval`) selama masih menunggu lock dari deployment lain.
  - Menghormati pembatalan context (`ctx.Done()`).
  - Pelepasan lock (`UnlockAdvisory`) dijalankan melalui `defer` menggunakan `context.Background()` agar tetap melepas lock meskipun `ctx` utama dibatalkan.

## Skema Tabel Tracking & Mode Read-Only `Status`

`EnsureTrackingTable` menyusun nama tabel dengan `pgx.Identifier{schema, table}.Sanitize()` (aman dari SQL injection identifier) dan membuat tabel:

```sql
CREATE TABLE IF NOT EXISTS "public"."schema_migrations" (
	version       VARCHAR(64)  PRIMARY KEY,
	filename      VARCHAR(255) NOT NULL,
	checksum      CHAR(64)     NOT NULL,
	service_name  VARCHAR(100) NOT NULL,
	triggered_by  VARCHAR(150) NOT NULL,
	git_branch    VARCHAR(100) NOT NULL,
	git_commit    VARCHAR(64)  NOT NULL,
	execution_ms  BIGINT       NOT NULL,
	applied_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
)
```

Pada `ListApplied`, jika PostgreSQL mengembalikan error kode `42P01` (`undefined_table`), `PostgresStore` mengembalikan slice kosong `nil, nil`. Hal ini memastikan perintah `pg-migrate status` tetap **100% read-only** bahkan pada database yang belum pernah di-baseline atau di-migrate.

## Perilaku Eksekusi (`ExecInTx` vs `ExecNoTx`)

Durasi timeout dikonversi oleh `FormatPostgresDuration` ke string milidetik (`"5000ms"`, `"60000ms"`):

- **`ExecInTx` (`-- migrate:transaction true`, default)**:
  1. Memulai transaksi `s.conn.Begin(ctx)` dengan `defer tx.Rollback(context.Background())`.
  2. Menyetel timeout lokal transaksi: `SELECT set_config('lock_timeout', $1, true)` dan `SELECT set_config('statement_timeout', $1, true)` (parameter `true` berarti hanya berlaku sampai `COMMIT`/`ROLLBACK`).
  3. Mengeksekusi keseluruhan skrip `sql` dalam satu pemanggilan `tx.Exec`, mencatat `ExecutionMS`, menyisipkan baris ke tabel tracking di dalam transaksi yang sama, lalu `tx.Commit(ctx)`.
- **`ExecNoTx` (`-- migrate:transaction false`)**:
  1. Menyetel timeout level session: `SELECT set_config('lock_timeout', $1, false)` dan `SELECT set_config('statement_timeout', $1, false)`.
  2. Menjadwalkan `defer` pemulihan sesi (`RESET lock_timeout; RESET statement_timeout;`) dengan context timeout 5 detik.
  3. Mengeksekusi statement yang telah dipecah oleh `migration.SplitStatements` satu per satu langsung di `s.conn`, lalu memanggil `InsertRecord`.

## Logika `Baseline`

- Mengecek file `00000000000000_*.sql` dan validitas lint dasarnya.
- Mengambil advisory lock dan memeriksa `ListApplied`:
  - Jika versi `00000000000000` sudah tercatat dengan checksum sama → `AlreadyBaselined = true` (*idempotent*).
  - Jika checksum berbeda → mengembalikan error checksum mismatch.
  - Jika `ApplySQL == false` (mode DB existing) → langsung memanggil `InsertRecord` tanpa mengeksekusi SQL.
  - Jika `ApplySQL == true` → menolak eksekusi apabila `len(appliedList) > 0`, lalu menjalankan SQL baseline melalui `ExecInTx` / `ExecNoTx`.
