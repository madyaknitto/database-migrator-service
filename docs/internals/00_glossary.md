# Glossary

Daftar istilah inti yang digunakan di seluruh kode sumber dan dokumentasi `database-migrator-service`.

## Arsitektur & Branching

| Istilah | Definisi |
|---|---|
| **Central Migration Repo** | Repository tunggal (`database-migrator-service`) yang menyimpan seluruh definisi target database, file migrasi SQL, dan runner Go `pg-migrate`. |
| **Registry (`databases.yaml`)** | File konfigurasi YAML yang mendaftarkan database target, directory SQL, override env var, dan nilai default timeout. |
| **Baseline** | Snapshot schema awal database tanpa data (`00000000000000_<deskripsi>.sql` di folder `baseline/`) sebagai titik nol sebelum migrasi incremental dimulai. |
| **Set-Based Tracking** | Pelacakan versi per file (bukan angka versi tunggal). Runner membandingkan himpunan file lokal terhadap baris di `public.schema_migrations`. |
| **Out-of-Order Execution** | Kemampuan menjalankan file migrasi ber-timestamp lebih lama (mis. `feat/1`) setelah file dengan timestamp lebih baru (`feat/2`) sudah lebih dulu terpasang di `production`. |
| **Expand–Contract** | Pola perubahan skema multi-tahap (*backward-compatible*): tambah kolom/struktur baru (*Expand*) → deploy seluruh layanan backend → hapus struktur lama (*Contract*). |

## Eksekusi & Locking

| Istilah | Definisi |
|---|---|
| **Advisory Lock** | Session lock di PostgreSQL (`pg_try_advisory_lock` / `pg_advisory_unlock`) dengan key 64-bit deterministik per database agar eksekusi paralel tidak bertabrakan. |
| **Dedicated Connection** | Koneksi tunggal `*pgx.Conn` per database yang dibuka oleh [`PostgresStore`](../../internal/runner/postgres_store.go) supaya session lock, session timeout, dan transaksi berada di session PostgreSQL yang sama. |
| **Lock Wait Timeout (`lock_wait_timeout`)** | Batas waktu tunggu runner saat polling `pg_try_advisory_lock` sebelum gagal (default `120s`). |
| **Lock Timeout (`lock_timeout`)** | Batas waktu tunggu PostgreSQL untuk memperoleh lock tabel/baris per statement DDL (default `5s`) agar tidak menahan antrian query aplikasi. |
| **Statement Timeout (`statement_timeout`)** | Batas durasi maksimal eksekusi satu statement/transaksi DDL di PostgreSQL (default `60s`). |
| **No-Tx Mode (`--no-tx`)** | Mode eksekusi dengan header `-- migrate:transaction false` di mana statement dipecah dan dijalankan di luar blok `BEGIN/COMMIT` (wajib untuk `CREATE INDEX CONCURRENTLY`). |

## Tracking & Validasi

| Istilah | Definisi |
|---|---|
| **Tracking Table** | Tabel riwayat migrasi di tiap database target (default `public.schema_migrations`) berisi `version`, `filename`, `checksum`, metadata git/pemicu, dan durasi eksekusi. |
| **Checksum (SHA-256)** | Hash SHA-256 dari isi SQL yang line ending-nya sudah dinormalisasi (`\r\n` dan `\r` → `\n`) untuk mendeteksi modifikasi file yang sudah dieksekusi. |
| **Directive (`-- migrate:*`)** | Anotasi komentar di bagian header file SQL sebelum statement pertama untuk mengatur service, tiket, mode transaksi, timeout, atau izin DDL destruktif/blocking. |
| **DB_ONLY (`APPLIED_IN_DB_NOT_IN_BRANCH`)** | Kondisi saat sebuah versi sudah tercatat di database (mis. dari fitur lain di `development`/`staging`), tetapi file-nya tidak ada di branch yang sedang dijalankan. |
