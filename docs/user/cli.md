# Referensi CLI (`pg-migrate`)

CLI `pg-migrate` berada di [`cmd/pg-migrate/main.go`](../../cmd/pg-migrate/main.go) dan menyediakan subcommand untuk mengelola validasi, baseline, pembuatan template file migrasi, pemeriksaan status, dan eksekusi migrasi.

```text
pg-migrate <perintah> [opsi]
```

## Ringkasan Perintah

| Perintah | Menyentuh DB? | Fungsi |
|---|---|---|
| `up` | Ya | Validasi SQL, ambil advisory lock, cek checksum, dan jalankan migrasi pending |
| `status` | Ya *(read-only)* | Tampilkan status tiap file migrasi dan `DB_ONLY` tanpa mengubah tabel/data |
| `validate` | Tidak | Validasi format nama file, header directive, dan SQL safety linter |
| `create` | Tidak | Membuat file migrasi baru di `<dir>/migrations/` dengan timestamp UTC unik |
| `baseline` | Ya | Mencatat marker baseline pada DB existing, atau mengeksekusi SQL baseline pada DB kosong (`--apply-sql`) |
| `version` | Tidak | Menampilkan versi CLI (`pg-migrate v0.1.0 (commit: dev)`) |
| `help` | Tidak | Menampilkan bantuan penggunaan |

---

## `pg-migrate up`

Memvalidasi seluruh file SQL lebih dulu (jika gagal, runner berhenti sebelum membuka koneksi DB), mengambil PostgreSQL advisory lock, memastikan tabel `public.schema_migrations` tersedia, memverifikasi checksum file yang sudah terpasang, lalu mengeksekusi seluruh file pending secara berurutan berdasarkan versi timestamp.

### Flag

| Flag | Default | Keterangan |
|---|---|---|
| `--config` | `databases.yaml` | Path ke file konfigurasi registry |
| `--db` | `all` | Target database (`all` atau daftar dipisah koma, mis. `db_name,billing_db`) |
| `--dry-run` | `false` | Menampilkan daftar migrasi pending tanpa mengeksekusi SQL |
| `--strict` | `false` | Memperlakukan temuan `WARNING` SQL linter sebagai `ERROR` |
| `--triggered-by` | `MIGRATE_TRIGGERED_BY` / `BUILD_TAG` / `manual` | Identitas pemicu yang dicatat ke tabel tracking |
| `--git-branch` | `GIT_BRANCH` / `BRANCH_NAME` / `unknown` | Nama branch yang dicatat ke tabel tracking |
| `--git-commit` | `GIT_COMMIT` / `unknown` | SHA commit yang dicatat ke tabel tracking |

Kolom ringkasan akhir `up`: `DATABASE`, `ACTUAL DB`, `SUDAH ADA`, `PENDING`, `DIEKSEKUSI`, `STATUS` (`UP_TO_DATE`, `DRY_RUN_PENDING`, atau `APPLIED`).

---

## `pg-migrate status`

Membandingkan daftar file lokal (`baseline/` dan `migrations/`) terhadap tabel tracking di database secara *read-only* (tidak membuat tabel tracking jika belum ada).

### Flag

| Flag | Default | Keterangan |
|---|---|---|
| `--config` | `databases.yaml` | Path ke file konfigurasi |
| `--db` | `all` | Target database (`all` atau daftar dipisah koma) |

### Arti Nilai `STATE`

| State | Arti |
|---|---|
| `BASELINE` | File baseline cocok dengan record `00000000000000` di DB |
| `PENDING_BASELINE` | File baseline ada di disk, tetapi belum tercatat di tabel tracking |
| `APPLIED` | File migrasi sudah dieksekusi di DB dengan checksum SHA-256 yang sama |
| `PENDING` | File migrasi ada di branch ini dan belum pernah dieksekusi di DB |
| `CHECKSUM_MISMATCH` | File yang sudah tercatat di DB mengalami perubahan isi (perintah `status` akan keluar dengan error) |
| `DB_ONLY` | Versi sudah tercatat di DB, tetapi file-nya tidak ada di branch saat ini (normal pada `development`/`staging` multi-branch) |

---

## `pg-migrate validate`

Menjalankan pemeriksaan struktur folder, validasi nama file, parsing directive header, dan SQL safety linter tanpa membutuhkan koneksi PostgreSQL.

| Flag | Default | Keterangan |
|---|---|---|
| `--config` | `databases.yaml` | Path ke file konfigurasi |
| `--db` | `all` | Target database (`all` atau daftar dipisah koma) |
| `--strict` | `false` | Menjadikan `WARNING` sebagai `ERROR` (exit code `1`) |

---

## `pg-migrate create`

Membuat file SQL baru di `<dir>/migrations/` dengan pola nama `YYYYMMDDHHmmss_<service>_<deskripsi>.sql`. Jika dipanggil beruntun pada detik UTC yang sama, runner otomatis menggeser timestamp +1 detik agar tidak pernah bentrok.

| Flag | Wajib | Default | Keterangan |
|---|---|---|---|
| `--config` | Tidak | `databases.yaml` | Path ke file konfigurasi |
| `--db` | **Ya** | `""` | Harus tepat 1 nama database yang terdaftar & aktif |
| `--service` | **Ya** | `""` | Nama service/domain (`^[a-z][a-z0-9-]*$`) |
| `--name` | **Ya** | `""` | Deskripsi singkat *snake_case* (`^[a-z0-9]+(?:_[a-z0-9]+)*$`; spasi/`-` otomatis dinormalisasi ke `_`) |
| `--ticket` | Tidak | `""` | Nomor tiket/issue (menambahkan `-- migrate:ticket <nilai>`) |
| `--no-tx` | Tidak | `false` | Menulis `-- migrate:transaction false` dan `-- migrate:statement-timeout 10m` untuk operasi non-transaksional |

---

## `pg-migrate baseline`

Mencari tepat satu file `00000000000000_<deskripsi>.sql` di folder `baseline/`. Jika versi baseline sudah ada dengan checksum yang sama, perintah bersifat *idempotent* (melewati tanpa error).

| Flag | Default | Keterangan |
|---|---|---|
| `--config` | `databases.yaml` | Path ke file konfigurasi |
| `--db` | `all` | Target database (`all` atau daftar dipisah koma) |
| `--apply-sql` | `false` | Jika `false`: hanya mencatat marker di `public.schema_migrations` tanpa mengeksekusi SQL. Jika `true`: mengeksekusi SQL baseline lalu mencatat marker (ditolak jika tabel tracking sudah berisi migrasi lain). |
| `--triggered-by` | `MIGRATE_TRIGGERED_BY` / `BUILD_TAG` / `manual` | Identitas pemicu |
| `--git-branch` | `GIT_BRANCH` / `BRANCH_NAME` / `unknown` | Nama branch |
| `--git-commit` | `GIT_COMMIT` / `unknown` | SHA commit |

---

## Exit Code

- **`0`**: Sukses (termasuk *no-op* saat `0 pending` pada `up`, atau `validate` hanya menghasilkan `WARNING` tanpa `--strict`).
- **`1`**: Argumen/perintah tidak dikenal, konfigurasi tidak valid, SQL lint gagal, koneksi/lock/eksekusi PostgreSQL gagal, atau ditemukan `CHECKSUM_MISMATCH`.
