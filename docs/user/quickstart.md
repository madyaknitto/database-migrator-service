# Quickstart Lokal

Panduan ini membantu developer menjalankan PostgreSQL lokal dengan Docker, menerapkan baseline awal, membuat file migrasi baru, dan menguji eksekusi secara lokal.

## Prasyarat

- **Docker** (dengan `docker compose`) untuk menjalankan PostgreSQL 16 lokal.
- Salah satu dari:
  - **Go 1.23+** di host untuk menjalankan `go run ./cmd/pg-migrate`, atau
  - **Docker saja** (Opsi B: Full Docker) tanpa meng-install Go di laptop.

## 1. Menyalakan PostgreSQL Lokal

1. Salin [`.env.example`](../../.env.example) menjadi `.env` dan sesuaikan bila perlu (default lokal memakai `localhost:5432`, user `migrator_user`, password `change_me`, `POSTGRES_SSLMODE=disable`).
2. Jalankan container PostgreSQL dari [`docker-compose.local.yml`](../../docker-compose.local.yml):

   ```bash
   docker compose -f docker-compose.local.yml up -d
   ```

   Saat pertama kali menyala, [`scripts/init-local-postgres.sql`](../../scripts/init-local-postgres.sql) otomatis:
   - Membuat role `migrator_user` (password `change_me`) dan `app_user` (password `app_local_password`).
   - Membuat database `db_name` dengan owner `migrator_user`.
   - Menyetel `ALTER DEFAULT PRIVILEGES` agar tabel/sequence baru dari `migrator_user` otomatis dapat diakses DML (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) oleh `app_user`.

   Untuk mereset database lokal kembali bersih dari nol:

   ```bash
   docker compose -f docker-compose.local.yml down -v
   docker compose -f docker-compose.local.yml up -d
   ```

## 2. Opsi A: Menjalankan `pg-migrate` dengan Go di Host

Set environment variable PostgreSQL di PowerShell lalu terapkan baseline pada database lokal yang masih kosong (`--apply-sql`):

```powershell
$env:POSTGRES_HOST="localhost"
$env:POSTGRES_PORT="5432"
$env:POSTGRES_USER="migrator_user"
$env:POSTGRES_PASSWORD="change_me"
$env:POSTGRES_SSLMODE="disable"
$env:GOTOOLCHAIN="local"

go run ./cmd/pg-migrate status --db=all
go run ./cmd/pg-migrate baseline --db=all --apply-sql
go run ./cmd/pg-migrate status --db=all
```

> **Penting:** Gunakan `--apply-sql` **hanya** pada database lokal/kosong. Untuk database staging & production yang sudah punya tabel, baca [Adopsi Database Existing](../operations/01_existing-db-adoption.md).

## 3. Opsi B: Full Docker (Tanpa Install Go di Host)

1. Build image runner dari [`Dockerfile`](../../Dockerfile):

   ```powershell
   docker build -t pg-migrate:local .
   ```

2. Buat fungsi shortcut `pg-migrate` sementara di PowerShell (folder `databases` di-mount read-write agar `create` dapat menulis file ke workspace lokal):

   ```powershell
   function pg-migrate {
     docker run --rm `
       -e POSTGRES_HOST=host.docker.internal `
       -e POSTGRES_PORT=5432 `
       -e POSTGRES_USER=migrator_user `
       -e POSTGRES_PASSWORD=change_me `
       -e POSTGRES_SSLMODE=disable `
       -v "${PWD}\databases.yaml:/app/databases.yaml:ro" `
       -v "${PWD}\databases:/app/databases" `
       pg-migrate:local @args
   }
   ```

   *(Untuk Linux/macOS Bash, ganti mount volume menjadi `-v "$(pwd)/databases.yaml:/app/databases.yaml:ro"` dan `-v "$(pwd)/databases:/app/databases"`; pada Linux native tambahkan `--add-host=host.docker.internal:host-gateway` bila diperlukan.)*

3. Terapkan baseline dan cek tabel di container PostgreSQL:

   ```powershell
   pg-migrate status --db=all
   pg-migrate baseline --db=all --apply-sql
   pg-migrate status --db=all
   docker exec db_migrator_service_postgres_local psql -U migrator_user -d db_name -c "\dt"
   ```

## 4. Membuat & Menguji Migrasi Baru

*(Jika memakai Opsi B Full Docker, ganti `go run ./cmd/pg-migrate` dengan `pg-migrate`.)*

1. Generate file migrasi baru dengan timestamp UTC otomatis:

   ```bash
   go run ./cmd/pg-migrate create --db=db_name --service=order --name=add_discount_column --ticket=PROJ-201
   ```

   Untuk operasi yang wajib berjalan di luar transaksi (seperti `CREATE INDEX CONCURRENTLY`), tambahkan `--no-tx`:

   ```bash
   go run ./cmd/pg-migrate create --db=db_name --service=order --name=add_idx_customer_id --no-tx
   ```

2. Isi file SQL baru dengan DDL yang *backward-compatible*:

   ```sql
   ALTER TABLE public.orders
   ADD COLUMN IF NOT EXISTS discount_amount NUMERIC(15, 2) NOT NULL DEFAULT 0;
   ```

3. Validasi linter, cek antrian, lakukan *dry-run*, lalu eksekusi:

   ```bash
   go run ./cmd/pg-migrate validate --db=all
   go run ./cmd/pg-migrate status --db=all
   go run ./cmd/pg-migrate up --db=all --dry-run
   go run ./cmd/pg-migrate up --db=all
   go run ./cmd/pg-migrate up --db=all   # run ulang: membuktikan 0 pending (no-op sukses)
   ```

4. Verifikasi skema hasil migrasi di container lokal:

   ```powershell
   docker exec db_migrator_service_postgres_local psql -U migrator_user -d db_name -c "\d public.orders"
   ```

## Langkah Selanjutnya

- Pelajari struktur registry di [Konfigurasi `databases.yaml`](./configuration.md).
- Pahami aturan penulisan SQL yang aman di [Header Directive & SQL Linter](./directives-and-linter.md).
