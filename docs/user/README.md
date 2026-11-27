# `database-migrator-service` User Guide

Selamat datang di dokumentasi pengguna **`database-migrator-service`**. Panduan ini menjelaskan cara kerja sistem migrasi PostgreSQL terpusat, prasyarat yang dibutuhkan, konsep inti, serta indeks seluruh panduan fitur.

## How `database-migrator-service` works

`database-migrator-service` mengelola perubahan skema PostgreSQL secara terpusat untuk banyak repository backend lintas stack (Node.js, Go, Python, Java, dll.) yang berbagi server PostgreSQL. Repository backend tidak menyimpan file SQL migrasi maupun meng-install binary migrator; setiap pipeline backend cukup memicu job Jenkins terpusat `database-migrator-service-run` sebelum deploy.

### Core concepts

- **Central Repo vs. Backend Repos**: Seluruh file migrasi SQL, registry [`databases.yaml`](../../databases.yaml), dan source code CLI [`pg-migrate`](../../cmd/pg-migrate/main.go) berada di satu repository ini. Repository backend hanya memicu job Jenkins terpusat saat deploy.
- **Set-Based Tracking (`public.schema_migrations`)**: `pg-migrate` mencatat setiap versi file (`YYYYMMDDHHmmss`) secara terpisah di tabel `public.schema_migrations`, bukan memakai angka versi tunggal. Fitur yang di-merge belakangan dengan timestamp lebih tua tetap aman dijalankan (*out-of-order execution*).
- **Baseline**: File `00000000000000_<deskripsi>.sql` di folder `baseline/` merepresentasikan titik nol skema database existing. Pada staging/production yang sudah memiliki tabel, `pg-migrate baseline` hanya mencatat marker tanpa mengeksekusi SQL.
- **Advisory Lock per Database**: Setiap target database diamankan oleh PostgreSQL session advisory lock (`pg_try_advisory_lock`) berdasarkan hash deterministik nama database, sehingga deploy bersamaan dari beberapa backend tidak pernah bertabrakan.
- **Pola Expand–Contract**: Karena banyak layanan backend bisa membaca database yang sama, DDL destruktif (`DROP TABLE`, `DROP COLUMN`, `RENAME`, `TRUNCATE`) diblokir secara default kecuali disertai alasan eksplisit pada header `-- migrate:allow-destructive`.

## Prerequisites and expectations

- **PostgreSQL dengan pemisahan role**: Runner memakai role DDL (`migrator_user`), sedangkan layanan backend memakai role DML (`app_user`) yang mendapat hak otomatis via `ALTER DEFAULT PRIVILEGES`.
- **Jenkins agent cukup punya Go 1.23+ atau Docker**: [Jenkinsfile](../../jenkins/Jenkinsfile) otomatis meng-compile `.bin/pg-migrate` saat build berjalan lalu menghapusnya di tahap `post`.
- **Satu repo per server/cluster PostgreSQL (pola template)**: Repo ini dirancang sebagai template repository — salin dari template setiap kali menyiapkan server atau domain database baru yang siklus rilisnya terpisah.
- **Immutable SQL setelah masuk staging/production**: File `.sql` yang sudah tereksekusi di database diverifikasi checksum SHA-256-nya; perubahan lanjutan wajib dibuat sebagai file migrasi baru.

## Quickstart

1. **Nyalakan PostgreSQL lokal** via Docker Compose (`docker compose -f docker-compose.local.yml up -d`).
2. **Terapkan baseline awal** pada database lokal yang masih kosong (`pg-migrate baseline --db=all --apply-sql`).
3. **Buat & validasi migrasi baru** (`pg-migrate create`, `pg-migrate validate`, `pg-migrate up --dry-run`, lalu `pg-migrate up`).
4. **Hubungkan ke Jenkins** dengan menyalin kredensial dan job `database-migrator-service-run` ([Setup Jenkins](../operations/02_jenkins-setup.md)).

---

## Documentation index

### Getting started

- [Quickstart lokal](./quickstart.md) — Menyalakan PostgreSQL Docker lokal, opsi Go vs Full Docker, baseline, dan uji migrasi pertama.
- [Konfigurasi `databases.yaml`](./configuration.md) — Struktur konfigurasi, urutan resolusi environment variable DSN, penambahan database baru, dan pemakaian sebagai template.
- [FAQ](./faq.md) — Pertanyaan umum tentang multi-server (`dsn_env`), pola template, pemisahan role, dan mekanisme `baseline`.

### Authoring & workflow

- [Referensi CLI (`pg-migrate`)](./cli.md) — Daftar lengkap subcommand (`up`, `status`, `validate`, `create`, `baseline`, `version`), seluruh flag, dan kode status.
- [Header directive & SQL safety linter](./directives-and-linter.md) — Sintaks `-- migrate:*`, semua aturan linter, mode `--strict`, dan praktik aman Expand–Contract.
- [SOP branching & out-of-order execution](./branching-workflow.md) — Aturan branch `main`, `staging`, `production`, `feat/*`, revisi saat QA, dan alur paralel.

### CI/CD & operations

- [Integrasi repo backend Jenkins](./jenkins-integration.md) — Pemanggilan job `database-migrator-service-run` dari Jenkins Pipeline dan Freestyle Job backend.
- [Adopsi database existing (Baseline)](../operations/01_existing-db-adoption.md) — Runbook langkah demi langkah mengadopsi database staging & production yang sudah berjalan.
- [Troubleshooting & pemulihan insiden](../operations/03_troubleshooting.md) — Penanganan `CHECKSUM_MISMATCH`, `lock_timeout`, `NO_TX_REQUIRED`, dan refresh database staging.
