# Runbook: Jenkins CI/CD Setup

Runbook ini menjelaskan cara menyiapkan job Jenkins terpusat **`database-migrator-service-run`** dari [`jenkins/Jenkinsfile`](../../jenkins/Jenkinsfile).

## Prasyarat Jenkins Server / Agent

- Jenkins dengan plugin **Pipeline**, **Git**, dan **Credentials Binding**.
- Pada agent Jenkins yang menjalankan job, harus tersedia **salah satu** dari:
  - **Go 1.23+**, atau
  - **Docker** (Jenkinsfile otomatis melakukan fallback build memakai `golang:1.23-alpine`).
- Akses jaringan dari Jenkins agent ke Git repository dan server PostgreSQL `staging` / `production`.

## Langkah 1: Buat Jenkins Credentials

Tambahkan credentials berikut di **Manage Jenkins → Credentials**:

| Credential ID | Tipe | Isi |
|---|---|---|
| `git-database-migrator-service-readonly` | Username with password / SSH / Token | Akses baca ke repository Git `database-migrator-service` |
| `postgres-host-staging` | Secret text | Hostname atau IP server PostgreSQL staging |
| `postgres-host-production` | Secret text | Hostname atau IP server PostgreSQL production |
| `postgres-migrator-staging` | Username with password | Username (`migrator_user`) & password PostgreSQL staging |
| `postgres-migrator-production` | Username with password | Username (`migrator_user`) & password PostgreSQL production |

Pastikan `MIGRATIONS_REPO` dan `MIGRATIONS_REPO_CRED_ID` di [`jenkins/Jenkinsfile`](../../jenkins/Jenkinsfile#L43-L47) sesuai dengan URL repository Anda (saat ini menunjuk ke `https://github.com/madyaknitto/database-migrator-service.git`).

## Langkah 2: Buat Pipeline Job `database-migrator-service-run`

1. Di Jenkins Dashboard, klik **New Item** → beri nama **`database-migrator-service-run`** → pilih **Pipeline**.
2. Di bagian **Pipeline**:
   - **Definition**: `Pipeline script from SCM`
   - **SCM**: `Git`
   - **Repository URL** & **Credentials**: gunakan repo migrasi dan `git-database-migrator-service-readonly`
   - **Script Path**: `jenkins/Jenkinsfile`
3. Simpan dan jalankan build pertama kali dengan **Build with Parameters** (mis. `TARGET_ENV=staging`, `ACTION=validate`) agar Jenkins mendaftarkan parameter job:
   - `TARGET_ENV`: `staging` | `production`
   - `TARGET_DB`: `all` atau daftar dipisah koma
   - `ACTION`: `up` | `dry-run` | `status` | `validate` | `baseline`
   - `TRIGGERED_BY`: default `manual` (otomatis terisi saat dipicu job backend)
   - `STRICT_LINT`: boolean (jadikan WARNING linter sebagai ERROR)

## Penjelasan Stage pada `Jenkinsfile`

1. **Resolve Environment & Checkout**: Memetakan `TARGET_ENV` ke branch target (`staging` atau `production`), ID credentials, dan `POSTGRES_SSLMODE` (`disable` untuk staging, `require` untuk production), lalu melakukan `CleanBeforeCheckout` dan mencatat `RESOLVED_GIT_COMMIT`.
2. **Build `pg-migrate` Runner**: Meng-compile binary statis sementara ke `.bin/pg-migrate` (memakai Go host atau fallback Docker `golang:1.23-alpine`) dan memverifikasi `./.bin/pg-migrate version`.
3. **Validate SQL & Linter**: Menjalankan `./.bin/pg-migrate validate --config=databases.yaml --db=...` (plus `--strict` bila dicentang).
4. **Execute Database Action**: Dilewati bila `ACTION == 'validate'`. Mengikat `POSTGRES_HOST`, `POSTGRES_USER`, dan `POSTGRES_PASSWORD` via `withCredentials` lalu menjalankan `up`, `up --dry-run`, `status`, atau `baseline`.
5. **Post (`always` & `failure`)**: Selalu membersihkan `.bin/` setelah build selesai.

## Menyiapkan Server PostgreSQL Baru (Multi-Server)

Jika organisasi memiliki server PostgreSQL lain yang terpisah:
1. Buat repository baru dari template GitHub `madyaknitto/database-migrator-service`.
2. Sesuaikan `databases.yaml`, folder `databases/`, `scripts/init-local-postgres.sql`, `MIGRATIONS_REPO`, serta nama credentials di `jenkins/Jenkinsfile`.
3. Buat job Jenkins terpisah untuk server tersebut (mis. `billing-migrator-run`) lalu arahkan repo backend terkait ke job tersebut.
