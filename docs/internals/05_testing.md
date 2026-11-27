# Testing

Dokumen ini merangkum strategi unit test pada `database-migrator-service`, cakupan tiap file pengujian, dan cara menjalankannya.

## Menjalankan Seluruh Test Suite

Dari root repository (pada Windows set `GOTOOLCHAIN=local` jika perlu memakai toolchain lokal):

```bash
go vet ./...
go test -count=1 -v ./...
```

Atau menggunakan [`Makefile`](../../Makefile):

```bash
make vet
make test
```

## Cakupan Test per Paket

### 1. [`internal/config/config_test.go`](../../internal/config/config_test.go)
- `TestLoadDefaultsAndEnabledDefaultTrue`: memverifikasi penerapan nilai default (`public`, `schema_migrations`, `5s`, `60s`, `120s`) dan default `enabled: true` ketika tidak ditulis.
- `TestLoadValidationErrors`: table-driven test untuk error `tracking_schema`, `tracking_table`, durasi negatif, nama database duplikat, keyword terlarang `all`, missing `dir`, dan missing `database`/`dsn_env`.
- `TestSelectDatabases`: menguji selector `"all"`, `""`, single DB, multi DB dengan spasi/duplikat, DB tidak terdaftar, dan DB berstatus `enabled: false`.
- `TestResolveDSN`: memverifikasi prioritas `POSTGRES_*` → `PG_*` → `DB_*`, URL-encoding karakter spesial di password, override `database_env` & `dsn_env`, serta error saat host/user kosong.

### 2. [`internal/migration/parser_test.go`](../../internal/migration/parser_test.go)
- `TestChecksumNormalizedLineEndings`: memastikan file LF dan CRLF menghasilkan SHA-256 yang identik.
- `TestParseDirectives` & `TestParseDirectivesErrors`: memverifikasi semua key `-- migrate:*` serta penolakan directive tidak dikenal, boolean invalid, durasi negatif, dan `allow-destructive` tanpa alasan.
- `TestSplitStatementsPostgreSQL`: memastikan komentar `--`, nested block comment `/* /* ... */ */`, string literal `'a;b''c'`, blok `DO $$ ... $$`, dan tagged dollar-quote `$fn$ ... $fn$` tidak terpecah oleh `;` internal.
- `TestLoadDatabaseFiles` & `TestLoadDatabaseFilesRejectsInvalidOrDuplicate`: pengabaian `.gitkeep`, urutan versi ascending, penolakan versi duplikat, dan penolakan versi `00000000000000` di `migrations/`.

### 3. [`internal/migration/linter_test.go`](../../internal/migration/linter_test.go)
- `TestLintFile_CommentsAndStringsIgnored`: kata kunci destruktif di dalam komentar dan literal string tidak memicu false positive.
- `TestLintFile_ConcurrentIndexRules`: verifikasi `NO_TX_REQUIRED`, `CONCURRENT_WITHOUT_IF_NOT_EXISTS`, dan lolos bersih saat `-- migrate:transaction false` + `IF NOT EXISTS`.
- `TestLintFile_BlockingIndexNewVsExistingTable`: `CREATE INDEX` pada tabel baru di file yang sama bersih, sedangkan pada tabel existing memicu `BLOCKING_INDEX`.
- `TestLintFile_DropColumnVsDropDefaultAndNotNull`: membedakan `DROP COLUMN` / shorthand `DROP <col>` (`DESTRUCTIVE_DROP_COLUMN`) dari `DROP DEFAULT` / `DROP NOT NULL` / `DROP CONSTRAINT`, serta bypass via `-- migrate:allow-destructive`.
- `TestLintFile_AddColumnNotNullWithoutDefault`: deteksi `NOT NULL` tanpa `DEFAULT`.
- `TestLintFile_BaselineSkipsDestructiveChecks`: file baseline tidak memicu aturan destruktif/blocking.

### 4. [`internal/runner/runner_test.go`](../../internal/runner/runner_test.go)
Menggunakan `mockStore` in-memory (tanpa memerlukan instance PostgreSQL live) untuk memverifikasi kontrak `Store` dan mesin eksekusi:
- `TestUp_ZeroPendingNoOp`: eksekusi kedua dengan 0 pending tidak memanggil `ExecInTx`.
- `TestUp_OutOfOrderAndDBOnlyWarning`: eksekusi file ber-timestamp lebih lama (`09:00`) setelah file lebih baru (`14:00`) dan fitur lain (`16:00`) tercatat di DB, serta menghasilkan warning `APPLIED_IN_DB_NOT_IN_BRANCH`.
- `TestUp_ChecksumMismatch`: menolak file yang dimodifikasi setelah terpasang.
- `TestUp_LockContentionAndTimeout`: simulasi lock sibuk lalu berhasil (*retry*), pelepasan lock setelah selesai, serta error timeout saat lock terus dipegang.
- `TestUp_NoTxAndDryRun`: memastikan `--dry-run` tidak mengeksekusi SQL dan `-- migrate:transaction false` memanggil `ExecNoTx` dengan statement yang sudah terpecah.
- `TestUp_LintFailureNeverTouchesDB`: kegagalan lint tidak pernah memanggil konektor DB.
- `TestBaseline_MarkerOnlyAndApplySQL`: memverifikasi mode marker-only (*idempotent*) dan penolakan `--apply-sql` pada DB yang sudah punya riwayat migrasi.
- `TestFormatPostgresDuration`: konversi durasi Go ke milidetik PostgreSQL (`90000ms`, `5000ms`) dan penolakan durasi negatif.

> **Batasan Saat Ini:** Pengujian integrasi terhadap server PostgreSQL nyata dilakukan secara manual via container Docker pada [`docker-compose.local.yml`](../../docker-compose.local.yml) (lihat [Quickstart Lokal](../user/quickstart.md)).
