# Runbook: Reset & Refresh `development` / `staging` dari `production`

Runbook ini menjelaskan cara **menimpa (*reset* / *refresh*)** environment `development` atau `staging` secara berkala agar bersih dari sisa fitur yang batal atau eksperimen yang menumpuk, sehingga kondisi branch dan database kembali mendekati (atau identik dengan) `production`.

---

## Mengapa Aman Ditimpa Berkala?

Pada [SOP Branching](../user/branching-workflow.md), branch `development` dan `staging` adalah **integration branch** yang **tidak pernah di-merge ke `main` ataupun `production`**. Semua fitur yang siap rilis selalu di-merge langsung dari `feat/*` → `main` → `production`.

Karena sifatnya sebagai lingkungan uji/integrasi sementara, kedua lapisan pada `development` dan `staging` aman untuk dibersihkan dan ditimpa ulang secara berkala:

1. **Lapisan Git (`branch`):** Branch `development` atau `staging` di-reset agar kembali sejajar dengan `production` (atau `main`).
2. **Lapisan Database (PostgreSQL):** Database `development` atau `staging` ditimpa menggunakan dump terbaru dari `production` (yang memuat skema bersih sekaligus posisi tabel tracking `public.schema_migrations` milik `production`).

> **Catatan Pemisahan Repo Template:** Jika organisasi Anda memiliki beberapa server PostgreSQL dari template ini, jalankan proses reset secara terpisah pada masing-masing repo migrasi dan database terkait.

---

## Kebijakan Branch Protection (GitHub / Git)

Pastikan aturan proteksi branch di repository GitHub membedakan peran tiap branch:

| Branch | Proteksi Push / Force-Push | Alasan |
|---|---|---|
| `main` | **Dilindungi ketat** (PR wajib, dilarang force-push & hapus branch) | Sumber kebenaran fitur yang sudah lulus QA/siap produksi |
| `production` | **Dilindungi ketat** (hanya merge terverifikasi dari `main`, dilarang force-push) | Representasi live database produksi |
| `development` | **Force-push diizinkan khusus Maintainer / Tech Lead** | Memungkinkan reset berkala dari `production` / `main` |
| `staging` | **Force-push diizinkan khusus Maintainer / Tech Lead** | Memungkinkan reset berkala dari `production` / `main` |

---

## Pilih Skenario yang Sesuai

| Kondisi Saat Ini | Skenario yang Dipakai | Hasil Akhir |
|---|---|---|
| Ingin environment benar-benar bersih dari nol (membuang tabel/kolom fitur yang batal atau sudah lama menumpuk) | **[Skenario A (Direkomendasikan): Full Reset — Timpa Branch Git + Timpa DB](#skenario-a-direkomendasikan--full-reset-branch-git--database)** | Branch Git + DB `development`/`staging` 100% bersih mendekati `production` |
| Semua fitur yang sedang ter-merge di branch `development`/`staging` masih aktif, hanya butuh refresh data & base schema dari `production` | **[Skenario B: Refresh Database Saja](#skenario-b--refresh-database-saja-branch-git-tetap)** | DB ditimpa dari `production`, lalu `pg-migrate up` otomatis me-*replay* migrasi fitur aktif di branch |
| Hanya mereset branch Git tanpa menyentuh database | **[Skenario C: Reset Branch Git Saja (Tidak Disarankan untuk Pembersihan Penuh)](#skenario-c--reset-branch-git-saja-tanpa-menimpa-database)** | Aman dijalankan (`exit 0`), tetapi sisa tabel/baris `DB_ONLY` dari fitur batal tetap tertinggal di DB |

---

## Checklist Keamanan Sebelum Menimpa Database

Lakukan 5 pengecekan wajib berikut sebelum mengeksekusi restore/drop pada database:

1. **Verifikasi ganda host & nama database target:** Pastikan target benar-benar server **`development`** atau **`staging`**, **JANGAN pernah** mengarah ke `production`.
2. **Koordinasikan downtime sementara:** Hentikan sementara atau beri pemberitahuan kepada layanan backend & tim QA/FE pada environment target selama proses restore berlangsung.
3. **Masking / anonimisasi data sensitif (PII):** Jika membawa data dari `production` ke `development` atau `staging`, lakukan *anonymization/masking* pada data sensitif (atau gunakan `--schema-only` ditambah script seed dummy).
4. **Pastikan role & `ALTER DEFAULT PRIVILEGES` tetap aktif:** Jika proses restore melakukan `DROP DATABASE` / `CREATE DATABASE` atau memakai opsi `--clean`, pastikan hak `migrator_user` dan `app_user` dipasang kembali (lihat [Runbook Adopsi Existing Database](./01_existing-db-adoption.md)).
5. **Verifikasi status migrasi:** Selalu akhiri dengan menjalankan `ACTION=status` di Jenkins untuk memastikan sinkronisasi branch dan database.

---

## Skenario A (Direkomendasikan) — Full Reset: Branch Git + Database

Gunakan langkah berikut untuk membersihkan total `development` atau `staging` agar kembali identik dengan `production`.

*(Contoh perintah di bawah memakai target `development`. Untuk target `staging`, cukup ganti `development` menjadi `staging`.)*

### Langkah 1: Timpa Branch Git dari `production` (atau `main`)

```bash
git fetch origin
git checkout development
git reset --hard origin/production   # atau origin/main jika ingin menyertakan fitur siap-rilis di main
git push --force-with-lease origin development
```

> Job Jenkins [`jenkins/Jenkinsfile`](../../jenkins/Jenkinsfile) sudah mengaktifkan `CleanBeforeCheckout` dan `PruneStaleBranch`, sehingga build Jenkins berikutnya otomatis mengikuti commit hasil reset tanpa konflik cache lokal.

### Langkah 2: Timpa Database Target dari Dump `production`

Ambil snapshot dari server `production` dan restore ke server target (`development` atau `staging`):

- **Opsi skema bersih + seed dummy (paling aman untuk data sensitif):** gunakan `pg_dump --schema-only` dari `production` (atau snapshot dump yang sudah disanitasi), lalu restore ke DB `development`/`staging` dan jalankan seed dummy lokal tim Anda.
- **Opsi full dump (setelah masking PII):** restore dump `production` ke DB target.

Karena tabel `public.schema_migrations` berada di dalam database yang sama, daftar migrasi yang tercatat di `development`/`staging` otomatis ikut ter-reset ke posisi `production`.

### Langkah 3: Pasang Ulang Role & Default Privileges (Jika Perlu)

Jika proses restore menimpa ownership atau membuat ulang database, jalankan ulang SQL berikut sebagai superuser/DBA pada database target:

```sql
GRANT USAGE, CREATE ON SCHEMA public TO migrator_user;
GRANT USAGE ON SCHEMA public TO app_user;

ALTER DEFAULT PRIVILEGES FOR ROLE migrator_user IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;

ALTER DEFAULT PRIVILEGES FOR ROLE migrator_user IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
```

### Langkah 4: Verifikasi di Jenkins

Jalankan job **`database-migrator-service-run`** melalui [Setup Jenkins CI/CD](./02_jenkins-setup.md):
1. `TARGET_ENV = development` *(atau `staging`)*, `TARGET_DB = all`, `ACTION = status` → pastikan status seluruh file `APPLIED` / `BASELINE` dan `pending=0, db_only=0`.
2. `TARGET_ENV = development` *(atau `staging`)*, `TARGET_DB = all`, `ACTION = up` → memastikan lock dan validasi berjalan mulus (`UP_TO_DATE`).

### Langkah 5: Merge Ulang Hanya Fitur yang Masih Aktif

Jika ada branch `feat/*` yang masih aktif dikerjakan dan perlu diuji kembali pada environment yang baru saja dibersihkan:
1. Pastikan branch `feat/*` tersebut berasal dari (atau sudah di-rebase/merge dengan) `main` terbaru.
2. Merge kembali **hanya** `feat/*` yang masih aktif ke branch `development` (atau `staging`).
3. Jalankan job **`database-migrator-service-run`** dengan `ACTION = up` (atau deploy layanan backend terkait) untuk menerapkan migrasi fitur-fitur aktif tersebut di atas kondisi bersih `production`.

---

## Skenario B — Refresh Database Saja (Branch Git Tetap)

Gunakan skenario ini jika seluruh fitur di branch `development` atau `staging` masih valid dan aktif, dan Anda hanya ingin menyegarkan isi/skema dasar database dari dump `production`.

1. Lakukan **Langkah 2** dan **Langkah 3** dari Skenario A (timpa DB target dari dump `production` dan pastikan privileges aktif).
2. Jalankan job **`database-migrator-service-run`**:
   - `TARGET_ENV = development` *(atau `staging`)*
   - `TARGET_DB = all`
   - `ACTION = up`
3. Berkat pelacakan *set-based* `pg-migrate`, runner akan membandingkan file di branch `development`/`staging` terhadap `public.schema_migrations` hasil dump `production`, lalu otomatis mengeksekusi ulang (*replay*) seluruh file migrasi fitur aktif secara berurutan.

---

## Skenario C — Reset Branch Git Saja (Tanpa Menimpa Database)

Jika branch `development`/`staging` di-`git reset --hard` ke `production` **tanpa** menimpa databasenya:

- Perintah `pg-migrate up` dan `pg-migrate status` tetap berjalan normal (**`exit 0`**) untuk fitur baru berikutnya.
- Namun untuk migrasi fitur lama/batal yang masih tercatat di database target tetapi sudah tidak ada di branch hasil reset, `pg-migrate` akan mengeluarkan status **`DB_ONLY`** dan warning **`APPLIED_IN_DB_NOT_IN_BRANCH`**.
- Selain itu, tabel atau kolom fisik sisa fitur yang batal tetap ada di database target sampai database ikut ditimpa (atau dibersihkan manual).

Oleh karena itu, untuk pembersihan berkala agar environment benar-benar kembali dekat ke `production`, selalu utamakan **Skenario A (Full Reset Branch Git + Database)** sesuai dokumentasi di [Troubleshooting](./03_troubleshooting.md).
