# FAQ (`database-migrator-service`)

### 1. Apakah 1 repository ini untuk 1 server database saja?
Secara default **ya** — seluruh entri di [`databases.yaml`](../../databases.yaml) memakai 1 server PostgreSQL bersama melalui `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, dan `POSTGRES_PASSWORD`. Namun jika di dalam domain yang sama ada satu database di host terpisah, entri tersebut bisa memakai field opsional `dsn_env` untuk menunjuk full connection string sendiri.

Untuk server PostgreSQL atau tim yang jadwal rilis dan hak aksesnya independen, pola yang disarankan adalah menduplikasi repository ini sebagai **GitHub Template Repository** (1 repo = 1 server/cluster PostgreSQL).

### 2. Apakah binary `pg-migrate` harus di-install di server atau di setiap repo backend?
**Tidak.** Repo backend tidak perlu menyimpan SQL migrasi maupun binary `pg-migrate`. Semua proses compile sementara (`.bin/pg-migrate`), validasi, eksekusi, dan pembersihan dijalankan otomatis oleh [`jenkins/Jenkinsfile`](../../jenkins/Jenkinsfile) di job `database-migrator-service-run`. Agent Jenkins cukup memiliki **Go 1.23+** atau **Docker**.

### 3. Apakah `pg-migrate baseline` menimpa tabel yang sudah ada di `development`/`staging`/`production`?
**Tidak — selama Anda TIDAK menambahkan flag `--apply-sql`.** Tanpa `--apply-sql`, perintah `pg-migrate baseline` tidak mengeksekusi file SQL baseline sama sekali; ia hanya membuat tabel `public.schema_migrations` dan memasukkan 1 baris marker versi `00000000000000`. Flag `--apply-sql` hanya dipakai untuk database lokal/baru yang masih kosong, dan otomatis ditolak jika tabel tracking sudah memiliki riwayat migrasi lain.

### 4. Mengapa harus memisahkan `migrator_user` dan `app_user`?
Agar layanan backend hanya memiliki hak DML (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) dan tidak pernah bisa melakukan `DROP`/`ALTER` tabel secara tidak sengaja. Sebaliknya, tabel baru yang dibuat oleh `migrator_user` otomatis dapat diakses aplikasi berkat `ALTER DEFAULT PRIVILEGES` yang disiapkan saat onboarding database.

### 5. Apa yang terjadi jika saya mengedit file `.sql` yang sudah sempat dijalankan di `development` atau `staging`?
Runner akan menolak eksekusi dengan error `CHECKSUM_MISMATCH`, karena isi file yang dinormalisasi (`\r\n` → `\n`) menghasilkan hash SHA-256 berbeda dari yang tercatat di `public.schema_migrations`. Kembalikan file lama seperti semula, lalu buat file migrasi baru (`pg-migrate create`) untuk revisinya.

### 6. Bagaimana jika ada 2 repo backend melakukan deploy ke Jenkins pada detik yang sama?
Aman. Pertama, `Jenkinsfile` mengaktifkan `disableConcurrentBuilds(abortPrevious: false)`. Kedua, di level database `pg-migrate` membuka 1 koneksi khusus per database dan mengambil PostgreSQL session advisory lock (`pg_try_advisory_lock`). Proses kedua menunggu lock dilepas (hingga `lock_wait_timeout`, default `120s`), kemudian memeriksa tabel tracking dan melihat antrian sudah `0 pending`.

### 7. Apakah branch dan database `development` / `staging` bisa ditimpa ulang dari `production` agar bersih lagi?
**Ya, aman dan memang disarankan secara berkala.** Karena `development` dan `staging` adalah *integration branch* yang tidak pernah di-merge ke `production`, Anda dapat mereset branch Git (`development`/`staging`) ke `production`, menimpa databasenya dari dump `production`, lalu menjalankan `pg-migrate up` (dan me-merge ulang hanya `feat/*` yang masih aktif). Lihat langkah lengkapnya di [Runbook Reset & Refresh Environment](../operations/04_env-reset-and-refresh.md).
