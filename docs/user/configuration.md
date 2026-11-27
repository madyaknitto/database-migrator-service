# Konfigurasi `databases.yaml` & Resolusi Koneksi

Seluruh daftar database target dan konfigurasi bawaan didefinisikan di [`databases.yaml`](../../databases.yaml) dan diparsing oleh [`internal/config/config.go`](../../internal/config/config.go).

## Struktur `databases.yaml`

```yaml
version: 1

defaults:
  tracking_schema: public
  tracking_table: schema_migrations
  lock_timeout: 5s
  statement_timeout: 60s
  lock_wait_timeout: 120s

databases:
  - name: db_name
    database: db_name
    # database_env: POSTGRES_DB_NAME
    # dsn_env: POSTGRES_DSN_DB_NAME
    dir: databases/db_name
    description: Database utama aplikasi PostgreSQL
    enabled: true
```

### Field pada `defaults`

| Field | Nilai Default | Aturan Validasi | Fungsi |
|---|---|---|---|
| `tracking_schema` | `public` | `^[a-zA-Z_][a-zA-Z0-9_]*$` | Schema tempat tabel tracking migrasi berada |
| `tracking_table` | `schema_migrations` | `^[a-zA-Z_][a-zA-Z0-9_]*$` | Nama tabel riwayat migrasi per database |
| `lock_timeout` | `5s` | Durasi Go positif (`> 0`) | Batas tunggu lock objek PostgreSQL per statement |
| `statement_timeout` | `60s` | Durasi Go positif (`> 0`) | Batas eksekusi SQL per statement/transaksi |
| `lock_wait_timeout` | `120s` | Durasi Go positif (`> 0`) | Batas tunggu polling PostgreSQL advisory lock |

### Field pada Setiap Entri `databases[]`

| Field | Wajib | Keterangan |
|---|---|---|
| `name` | Ya | Identifier logis untuk CLI `--db`. Unik, tidak boleh `all`, pola `^[a-zA-Z0-9_-]+$`. |
| `database` | Ya *(kecuali `dsn_env` selalu terisi)* | Nama database PostgreSQL aktual di server. |
| `database_env` | Tidak | Nama environment variable untuk meng-override nama database aktual per environment (mis. `db_name_dev` / `db_name_staging`). |
| `dsn_env` | Tidak | Nama environment variable berisi full DSN PostgreSQL jika database berada di server terpisah. |
| `dir` | Ya | Path direktori berisi subfolder `baseline/` dan `migrations/` (relatif terhadap lokasi `databases.yaml` atau absolut). |
| `description` | Tidak | Catatan deskriptif kegunaan database. |
| `enabled` | Tidak | Default otomatis `true` jika tidak dicantumkan. Entri `enabled: false` dilewati oleh `--db=all` dan ditolak jika dipanggil langsung. |

## Urutan Resolusi Koneksi (`ResolveDSN`)

Untuk setiap database terpilih, runner menentukan DSN koneksi dengan urutan prioritas berikut:

1. **Full DSN override (`dsn_env`)**: Jika `dsn_env` dikonfigurasi dan env var tersebut terisi nilai non-kosong, full connection string langsung dipakai (nama DB aktual diambil dari path DSN, dengan fallback ke `database` lalu `name`).
2. **Nama database aktual (`database_env` → `database`)**: Jika `database_env` disetel di env, nilainya menimpa field `database`.
3. **Variabel server PostgreSQL bersama**: Dibangun URL `postgres://...` menggunakan prioritas env var:
   - **Host (wajib)**: `POSTGRES_HOST` → `PG_HOST` → `DB_HOST`
   - **User (wajib)**: `POSTGRES_USER` → `PG_USER` → `DB_USER`
   - **Port**: `POSTGRES_PORT` → `PG_PORT` → `DB_PORT` (default `5432`, rentang valid `1`–`65535`)
   - **Password**: `POSTGRES_PASSWORD` → `PG_PASSWORD` → `DB_PASSWORD` (di-URL-encode otomatis oleh `url.UserPassword`)
   - **SSL Mode**: `POSTGRES_SSLMODE` → `PG_SSLMODE` → `DB_SSLMODE` (default `disable`)

## Menyesuaikan Template `db_name` & Menambah Database

### Mengganti Template `db_name`
Saat memakai repository ini untuk proyek baru:
1. Ganti `db_name` di [`databases.yaml`](../../databases.yaml) dengan nama database proyek Anda.
2. Rename folder [`databases/db_name`](../../databases/db_name) agar sesuai dengan field `dir`.
3. Perbarui `CREATE DATABASE` dan `\connect` pada [`scripts/init-local-postgres.sql`](../../scripts/init-local-postgres.sql) untuk pengujian Docker lokal.

### Menambah Database Baru di Server yang Sama
1. Siapkan role `migrator_user` dan `ALTER DEFAULT PRIVILEGES` untuk `app_user` di database baru tersebut.
2. Tambahkan entri baru pada `databases:` di [`databases.yaml`](../../databases.yaml).
3. Jalankan `./scripts/dump-baseline.sh <nama_db>` jika database sudah memiliki tabel eksisting, atau buat migrasi pertama dengan `pg-migrate create --db=<nama_db> ...`.

## Menggunakan Repository sebagai Template

Repository ini di-set sebagai **GitHub Template Repository**. Untuk server PostgreSQL baru dengan jadwal rilis terpisah:
1. Buat repository baru melalui tombol **Use this template** di GitHub.
2. Sesuaikan `databases.yaml`, folder `databases/`, `scripts/init-local-postgres.sql`, dan URL/kredensial pada [`jenkins/Jenkinsfile`](../../jenkins/Jenkinsfile).
