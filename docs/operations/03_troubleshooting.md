# Runbook: Troubleshooting & Incident Recovery

Panduan diagnosa cepat dan langkah pemulihan saat validasi atau eksekusi migrasi menemui kendala.

## Matriks Masalah Umum

| Gejala / Kode Error | Penyebab | Tindakan Pemulihan |
|---|---|---|
| `checksum mismatch pada <file>` / `CHECKSUM_MISMATCH` | File `.sql` yang sudah tercatat di `public.schema_migrations` diedit ulang isinya | Lihat bagian [Pemulihan Checksum Mismatch](#pemulihan-checksum-mismatch) |
| `canceling statement due to lock timeout` (`55P03`) | Ada transaksi lama di aplikasi backend yang memegang lock pada tabel target | Ulangi job setelah beban transaksi mereda; untuk pembuatan index gunakan `CREATE INDEX CONCURRENTLY IF NOT EXISTS` dengan `--no-tx` |
| `canceling statement due to statement timeout` (`57014`) | DDL memakan waktu lebih lama dari `statement_timeout` (default `60s`) | Untuk operasi lama yang aman (mis. `CREATE INDEX CONCURRENTLY`), tambahkan `-- migrate:statement-timeout 10m` di header file |
| `NO_TX_REQUIRED` saat `validate` / `up` | Statement `CONCURRENTLY` atau `VACUUM` dijalankan dengan default `transaction: true` | Tambahkan `-- migrate:transaction false` dan `IF NOT EXISTS` di header file |
| `timeout menunggu advisory lock (key=...) setelah 120s` | Proses migrasi lain masih berjalan lama atau ada sesi lama menggantung | Cek koneksi aktif di `pg_stat_activity` / `pg_locks` pada PostgreSQL, tunggu proses sah selesai sebelum retry |
| `APPLIED_IN_DB_NOT_IN_BRANCH` *(Warning)* | Di `development`/`staging`, ada versi dari branch fitur lain yang sudah dieksekusi di DB | Normal di `development`/`staging` (bukan error). Jika terjadi di `production`, audit asal versi dengan `pg-migrate status` |
| `menolak --apply-sql karena tabel tracking sudah memiliki N entri` | Menjalankan `baseline --apply-sql` pada database yang sudah memiliki riwayat migrasi | Gunakan `pg-migrate baseline` **tanpa** `--apply-sql` untuk DB existing |
| `CREATE INDEX CONCURRENTLY` gagal di tengah jalan | Kegagalan menyisakan *invalid index* di PostgreSQL | Hapus invalid index (`DROP INDEX CONCURRENTLY IF EXISTS ...` dengan `-- migrate:transaction false`), perbaiki penyebabnya, lalu jalankan ulang |

---

## Pemulihan `CHECKSUM_MISMATCH`

1. Cek versi yang bermasalah melalui `pg-migrate status --db=<db>`.
2. Kembalikan isi file lama tersebut agar sama persis dengan versi yang sudah pernah dieksekusi di DB (gunakan riwayat Git dari commit saat file pertama kali di-merge).
3. Jika ada perubahan tambahan yang ingin diterapkan (misalnya revisi selama integrasi di `development` atau QA di `staging`), buat file migrasi baru dengan `pg-migrate create --db=<db> --service=<svc> --name=<revisi>` di branch fitur yang sama.

> **Catatan Line Ending:** `pg-migrate` otomatis menormalisasi CRLF/CR menjadi LF sebelum menghitung SHA-256, sehingga perbedaan line ending Windows/Linux **tidak** memicu `CHECKSUM_MISMATCH`.

---

## Setelah Refresh / Reset Database `development` atau `staging` dari Dump `production`

Ketika tim infrastruktur menimpa/me-refresh database `development` atau `staging` menggunakan dump dari `production`, isi tabel `public.schema_migrations` di environment tersebut otomatis ikut ter-reset ke posisi `production`.

- **Jika hanya me-refresh database (branch Git `development`/`staging` tetap):**
  1. Jalankan job Jenkins **`database-migrator-service-run`** dengan parameter:
     - `TARGET_ENV = development` *(atau `staging`)*
     - `TARGET_DB = all`
     - `ACTION = up`
  2. Karena `pg-migrate` menggunakan *set-based tracking*, runner akan membandingkan file di branch target terhadap `public.schema_migrations` hasil dump `production` dan langsung menerapkan ulang seluruh file fitur aktif secara berurutan.
- **Jika ingin membersihkan total branch Git sekaligus databasenya agar identik dengan `production`:** ikuti panduan lengkap di [Runbook Reset & Refresh Environment](./04_env-reset-and-refresh.md).
