# Development & Local Builds

Runbook ini menjelaskan cara menyiapkan workspace lokal untuk mengembangkan `pg-migrate`, membangun binary, dan menjalankan container PostgreSQL pengujian.

## Kebutuhan Alat

- **Go 1.23+** (sesuai [`go.mod`](../../go.mod)).
- **Docker & Docker Compose** (untuk menjalankan PostgreSQL 16 lokal atau mem-build image runner).
- Opsional: `make` (Linux/macOS/Git Bash) dan `pg_dump` / `psql` bila ingin menjalankan script helper langsung di host.

## Perintah Build & Validasi Lokal

Dari root repository (di Windows PowerShell, set `$env:GOTOOLCHAIN="local"` bila diperlukan):

```powershell
$env:GOTOOLCHAIN="local"

# 1. Static analysis & unit tests
go vet ./...
go test -count=1 -v ./...

# 2. Validasi seluruh file SQL terhadap databases.yaml
go run ./cmd/pg-migrate validate --config=databases.yaml --db=all

# 3. Cek versi CLI
go run ./cmd/pg-migrate version
```

Target [`Makefile`](../../Makefile) yang tersedia:

| Target | Perintah | Keterangan |
|---|---|---|
| `make vet` | `go vet ./...` | Analisis statis seluruh paket |
| `make test` | `go test -v ./...` | Menjalankan seluruh unit test |
| `make build` | `CGO_ENABLED=0 go build -trimpath -o dist/pg-migrate ./cmd/pg-migrate` | Build binary statis untuk OS host |
| `make build-linux` | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/pg-migrate-linux-amd64 ./cmd/pg-migrate` | Cross-compile binary Linux amd64 |
| `make validate` | `go run ./cmd/pg-migrate validate --config=databases.yaml --db=all` | Validasi linter semua database aktif |
| `make clean` | `rm -rf dist bin` | Menghapus artefak build lokal |

Script [`scripts/build-linux.sh`](../../scripts/build-linux.sh) juga menjalankan cross-compile statis yang sama ke `dist/pg-migrate-linux-amd64`.

## Docker Image Runner (`Dockerfile`)

[`Dockerfile`](../../Dockerfile) menggunakan multi-stage build:
1. **Stage `builder` (`golang:1.23-alpine`)**: mengunduh dependensi Go lalu mem-build binary statis (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 -trimpath -ldflags="-s -w"`) ke `/out/pg-migrate`.
2. **Stage runtime (`alpine:3.20`)**: memasang `ca-certificates` & `tzdata`, membuat user non-root `migrator` (UID `10001`), menyalin binary ke `/usr/local/bin/pg-migrate` bersama `databases.yaml` dan `databases/`, lalu menyetel `ENTRYPOINT ["/usr/local/bin/pg-migrate"]`.

Build image lokal:

```bash
docker build -t pg-migrate:local .
```

## Mengelola PostgreSQL Lokal (`docker-compose.local.yml`)

[`docker-compose.local.yml`](../../docker-compose.local.yml) menjalankan `postgres:16-alpine` dengan nama container `db_migrator_service_postgres_local` pada port `5432`, timezone `UTC`, volume `db_migrator_service_postgres_data`, dan init script [`scripts/init-local-postgres.sql`](../../scripts/init-local-postgres.sql).

```bash
# Start PostgreSQL lokal
docker compose -f docker-compose.local.yml up -d

# Cek log atau masuk psql
docker exec -it db_migrator_service_postgres_local psql -U migrator_user -d db_name

# Reset bersih (hapus volume data agar init-local-postgres.sql berjalan ulang)
docker compose -f docker-compose.local.yml down -v
docker compose -f docker-compose.local.yml up -d
```
