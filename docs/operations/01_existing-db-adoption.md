# Runbook: Adopting Existing Databases (Baseline)

Gunakan runbook ini **sekali saja** saat mengadopsi database PostgreSQL `staging` dan `production` yang sudah memiliki tabel berjalan agar `database-migrator-service` bisa mulai mengelola migrasi incremental tanpa mengganggu data maupun tabel lama.

## Prasyarat

- Akses superuser/DBA ke database PostgreSQL `staging` dan `production` untuk menyiapkan role dan privileges awal.
- `pg_dump` terpasang di mesin yang mengambil snapshot skema produksi.
- Branch `main`, `staging`, dan `production` sudah tersedia di repository Git.

## Langkah 1: Siapkan Role & Default Privileges di PostgreSQL

Pastikan seluruh aplikasi backend terhubung menggunakan role DML (`app_user`), sedangkan runner `pg-migrate` menggunakan role DDL (`migrator_user`). Jalankan SQL berikut pada **setiap** database yang akan dikelola oleh superuser/DBA:

```sql
GRANT USAGE, CREATE ON SCHEMA public TO migrator_user;
GRANT USAGE ON SCHEMA public TO app_user;

-- Agar tabel & sequence baru yang dibuat pg-migrate otomatis bisa diakses layanan backend:
ALTER DEFAULT PRIVILEGES FOR ROLE migrator_user IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;

ALTER DEFAULT PRIVILEGES FOR ROLE migrator_user IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
```

## Langkah 2: Ambil Snapshot Schema Production

Gunakan [`scripts/dump-baseline.sh`](../../scripts/dump-baseline.sh) terhadap database `production` (sebagai sumber skema paling bersih) untuk menghasilkan `databases/<nama_db>/baseline/00000000000000_initial_baseline.sql`:

```bash
POSTGRES_HOST=pg-prod.internal \
POSTGRES_PORT=5432 \
POSTGRES_USER=migrator_user \
POSTGRES_PASSWORD='***' \
./scripts/dump-baseline.sh db_name
```

*(Jika nama entri di `databases.yaml` berbeda dengan nama database PostgreSQL aktual, berikan argumen kedua: `./scripts/dump-baseline.sh <nama_entri> <actual_pg_db>`.)*

Periksa hasil `databases/<nama_db>/baseline/00000000000000_initial_baseline.sql`, pastikan `pg-migrate validate --db=all` lulus, lalu commit ke `main` dan merge ke branch `staging` dan `production`.

## Langkah 3: Tandai Marker Baseline (TANPA `--apply-sql`)

Jalankan `pg-migrate baseline` **tanpa** flag `--apply-sql` terhadap environment `staging` dan `production` (bisa melalui job Jenkins `database-migrator-service-run` dengan memilih `ACTION=baseline`):

```bash
pg-migrate baseline --config=databases.yaml --db=all
```

Yang dilakukan perintah ini:
1. Mengambil advisory lock per database.
2. Membuat tabel `public.schema_migrations` jika belum ada.
3. Mencatat 1 baris versi `00000000000000` dengan `service_name = 'baseline'` dan checksum SHA-256 dari file baseline **tanpa mengeksekusi SQL baseline**.

> **Peringatan:** Jangan gunakan `--apply-sql` pada database staging/production existing. Jika dijalankan lagi secara tidak sengaja tanpa `--apply-sql`, perintah ini bersifat *idempotent* (`AlreadyBaselined = true`) selama isi file baseline tidak berubah.

## Langkah 4: Verifikasi

Jalankan status check untuk memastikan posisi awal sudah siap:

```bash
pg-migrate status --config=databases.yaml --db=all
```

Seluruh database harus menampilkan state `BASELINE` untuk versi `00000000000000`. Setelah titik ini, seluruh perubahan skema baru diletakkan di folder `migrations/` menggunakan `pg-migrate create`.
