# Header Directive & SQL Safety Linter

Untuk menjaga keamanan database produksi yang dipakai bersama oleh banyak layanan backend, setiap file SQL divalidasi oleh parser dan linter di [`internal/migration/`](../../internal/migration/linter.go) sebelum koneksi database dibuka.

## Aturan Penamaan File

- **Baseline** (`<dir>/baseline/`): maksimal **1 file** dengan pola `00000000000000_<deskripsi>.sql` (mis. `00000000000000_initial_baseline.sql`).
- **Migrasi incremental** (`<dir>/migrations/`): pola `YYYYMMDDHHmmss_<service>_<deskripsi>.sql` (versi `00000000000000` dilarang di `migrations/`).
  - `<service>`: huruf kecil diawali huruf, boleh angka dan `-` (`^[a-z][a-z0-9-]*$`).
  - `<deskripsi>`: huruf kecil/angka dipisah `_` (`^[a-z0-9]+(?:_[a-z0-9]+)*$`).
- File bantu berikut otomatis diabaikan oleh loader: `.gitkeep`, `README.md`, `README.txt`, `.DS_Store`.

## Header Directive (`-- migrate:*`)

Directive ditulis sebagai komentar `-- migrate:<key> <value>` di bagian awal file **sebelum statement SQL pertama** (komentar blok `/* ... */` dan komentar biasa di atasnya diabaikan; setelah statement SQL pertama dimulai, parsing directive berhenti):

| Directive | Default | Fungsi & Validasi |
|---|---|---|
| `-- migrate:service <nama>` | Diambil dari nama file | Menimpa nilai `service_name` yang dicatat ke tabel tracking |
| `-- migrate:ticket <kode>` | `""` | Metadata tiket/issue (mis. `PROJ-201`) untuk dokumentasi file |
| `-- migrate:transaction <true\|false>` | `true` | Mengatur apakah seluruh file dieksekusi dalam 1 transaksi (`BEGIN ... COMMIT`) atau statement-by-statement di luar transaksi (`no-tx`) |
| `-- migrate:lock-timeout <durasi>` | `defaults.lock_timeout` (`5s`) | Meng-override `lock_timeout` PostgreSQL untuk file ini (durasi Go positif, mis. `10s`) |
| `-- migrate:statement-timeout <durasi>` | `defaults.statement_timeout` (`60s`) | Meng-override `statement_timeout` PostgreSQL untuk file ini (mis. `10m` untuk `CREATE INDEX CONCURRENTLY`) |
| `-- migrate:allow-destructive <alasan>` | `""` | Wajib menyertakan alasan non-kosong untuk mengizinkan `DROP TABLE`, `DROP COLUMN`, `TRUNCATE`, atau `ALTER TABLE ... RENAME` |
| `-- migrate:allow-blocking-index <alasan>` | `""` | Wajib menyertakan alasan non-kosong untuk membungkam warning `BLOCKING_INDEX` (mis. untuk tabel kecil dev-only) |

> Directive `-- migrate:` yang tidak dikenal atau bernilai tidak valid akan langsung menghasilkan error saat load file.

## Daftar Aturan SQL Safety Linter

Linter otomatis mengabaikan kata kunci yang berada di dalam komentar SQL (`--`, `/* ... */`) maupun literal string (`'...'`, `E'...'`, `$$...$$`). File baseline (`IsBaseline=true`) melewati aturan destruktif/blocking karena berasal dari hasil `pg_dump`.

| Rule | Severity | Pemicu | Cara Mengatasi |
|---|---|---|---|
| `EMPTY_SQL` | `ERROR` | File tidak memiliki statement SQL bermakna | Isi minimal 1 statement SQL valid |
| `NO_TX_REQUIRED` | `ERROR` | `CREATE/DROP INDEX CONCURRENTLY`, `REINDEX ... CONCURRENTLY`, atau `VACUUM` dengan `transaction: true` | Tambahkan `-- migrate:transaction false` (atau buat dengan `pg-migrate create --no-tx`) |
| `DESTRUCTIVE_DROP_TABLE` | `ERROR` | `DROP TABLE` tanpa `-- migrate:allow-destructive` | Hapus jika tidak perlu, atau tambahkan `-- migrate:allow-destructive <alasan>` |
| `DESTRUCTIVE_DROP_COLUMN` | `ERROR` | `ALTER TABLE ... DROP COLUMN` atau shorthand `ALTER TABLE ... DROP <kolom>` tanpa `-- migrate:allow-destructive` *(tidak memicu untuk `DROP DEFAULT`, `DROP NOT NULL`, `DROP CONSTRAINT`)* | Terapkan pola Expand–Contract, lalu beri `-- migrate:allow-destructive <alasan>` pada fase Contract |
| `DESTRUCTIVE_TRUNCATE` | `ERROR` | `TRUNCATE` tanpa `-- migrate:allow-destructive` | Tambahkan `-- migrate:allow-destructive <alasan>` jika memang disengaja |
| `BREAKING_RENAME` | `ERROR` | `ALTER TABLE ... RENAME` tanpa `-- migrate:allow-destructive` | Gunakan pola Expand–Contract di lingkungan multi-repo |
| `BLOCKING_INDEX` | `WARNING` | `CREATE INDEX` pada tabel existing tanpa `CONCURRENTLY` *(aman untuk tabel yang baru dibuat oleh `CREATE TABLE` di file yang sama)* | Gunakan `CREATE INDEX CONCURRENTLY IF NOT EXISTS` + `-- migrate:transaction false`, atau `-- migrate:allow-blocking-index <alasan>` |
| `NOT_NULL_WITHOUT_DEFAULT` | `WARNING` | `ALTER TABLE ... ADD [COLUMN] ... NOT NULL` tanpa `DEFAULT` *(aman untuk `ADD PRIMARY KEY`/`UNIQUE`/`CHECK`/`FOREIGN`/`CONSTRAINT`)* | Tambahkan `DEFAULT`, atau buat kolom nullable dulu lalu backfill sebelum `SET NOT NULL` |
| `CONCURRENT_WITHOUT_IF_NOT_EXISTS` | `WARNING` | `CREATE INDEX CONCURRENTLY` tanpa `IF NOT EXISTS` | Tambahkan `IF NOT EXISTS` agar aman diulang jika sempat meninggalkan invalid index |

> Menjalankan `pg-migrate validate --strict` atau `pg-migrate up --strict` menaikkan semua `WARNING` menjadi `ERROR` yang memblokir eksekusi.

## Pola Expand–Contract (Zero-Downtime Multi-Repo)

Jika beberapa layanan backend membaca tabel yang sama dan Anda perlu mengubah atau menghapus kolom:
1. **Expand**: Tambah kolom baru yang nullable atau memiliki `DEFAULT`, buat index dengan `CREATE INDEX CONCURRENTLY IF NOT EXISTS`, dan lakukan backfill data bila perlu.
2. **Migrate Apps**: Deploy seluruh repository backend agar menulis & membaca kolom baru.
3. **Contract**: Setelah tidak ada lagi aplikasi yang memakai kolom lama, buat file migrasi terpisah untuk menghapus kolom/tabel lama dengan menyertakan `-- migrate:allow-destructive semua layanan backend sudah berpindah ke kolom baru`.
