# Architecture

Dokumen ini menjelaskan struktur arsitektur internal `database-migrator-service`, batas tanggung jawab tiap paket Go, dan alur eksekusi dari trigger Jenkins hingga commit ke PostgreSQL.

## Gambaran Arsitektur

```mermaid
flowchart TD
    subgraph BE["Backend Repositories (Multi-Stack)"]
        A["Repo Backend A"]
        B["Repo Backend B"]
        M["Trigger Manual (DBA / Developer)"]
    end

    subgraph J["Jenkins CI/CD"]
        JOB["Job Terpusat: database-migrator-service-run"]
        BLD["Build Binary Go (pg-migrate)"]
        VAL["Validate SQL Safety Linter"]
        RUN["Execute: pg-migrate up"]
        JOB --> BLD --> VAL --> RUN
    end

    subgraph REPO["Central Repo: database-migrator-service"]
        BR["Branch Target (staging / production)"]
        CFG["Registry: databases.yaml"]
        SQL["Folder SQL: databases/db_name/migrations/"]
        BR --> CFG
        BR --> SQL
    end

    subgraph PG["PostgreSQL Server"]
        subgraph DB1["Database: db_name"]
            L1["Advisory Lock (pg_try_advisory_lock)"]
            T1["Tabel: public.schema_migrations"]
        end
    end

    A -->|"Pre-Deploy (wait=true, propagate=true)"| JOB
    B -->|"Pre-Deploy (wait=true, propagate=true)"| JOB
    M -->|"Build with Parameters"| JOB

    BR -->|"Checkout sesuai TARGET_ENV"| BLD
    CFG -->|"Pilih DB target (all / spesifik)"| RUN
    SQL -->|"Hitung pending (set-based)"| RUN

    RUN -->|"Koneksi dedicated"| L1
    L1 --> T1
```

## Peta Paket & Batas Tanggung Jawab

| Paket | Lokasi | Tanggung Jawab | Dependensi Eksternal |
|---|---|---|---|
| **CLI (`main`)** | [`cmd/pg-migrate/main.go`](../../cmd/pg-migrate/main.go) | Parsing flag CLI (`up`, `status`, `validate`, `create`, `baseline`, `version`), membaca env metadata CI, dan memformat output terminal | Stdlib + paket `internal/*` |
| **Config** | [`internal/config/config.go`](../../internal/config/config.go) | Membaca & memvalidasi `databases.yaml`, menyeleksi database aktif, dan membangun DSN PostgreSQL dari env | `gopkg.in/yaml.v3` |
| **Migration** | [`internal/migration/`](../../internal/migration/parser.go) | Memuat file `baseline/` & `migrations/`, normalisasi line ending + SHA-256, parser directive, splitter statement PostgreSQL, dan safety linter statis | Stdlib saja (tanpa DB) |
| **Runner** | [`internal/runner/`](../../internal/runner/runner.go) | Orkestrasi advisory lock, cek riwayat & checksum, deteksi pending/DB_ONLY, eksekusi TX/no-TX melalui abstraksi `Store` (`PostgresStore`) | `github.com/jackc/pgx/v5` |

## Alur Eksekusi End-to-End (`pg-migrate up`)

```mermaid
sequenceDiagram
    autonumber
    participant CLI as cmd/pg-migrate
    participant CFG as internal/config
    participant MIG as internal/migration
    participant RUN as internal/runner
    participant PG as PostgresStore (pgx.Conn)

    CLI->>CFG: Load("databases.yaml") & SelectDatabases(selector)
    CFG->>CFG: ResolveAll(selected, os.LookupEnv)
    loop Per ResolvedDatabase
        CLI->>RUN: Up(ctx, ConnectPostgres, opts)
        RUN->>MIG: LoadDatabaseFiles(dir) & LintAll(files)
        alt Ada lint error (atau warning saat --strict)
            RUN-->>CLI: Return error sebelum membuka koneksi DB
        end
        RUN->>PG: ConnectPostgres(ctx, dsn) -> 1 *pgx.Conn
        RUN->>PG: AcquireLock via pg_try_advisory_lock(LockKey)
        RUN->>PG: EnsureTrackingTable & ListApplied
        RUN->>RUN: Validasi checksum & hitung pending (set-based)
        alt 0 pending atau --dry-run
            RUN-->>PG: UnlockAdvisory & Close
            RUN-->>CLI: Return UpResult (no-op / dry-run)
        else N file pending
            loop Setiap file pending (urut timestamp)
                alt Directives.Transaction == true
                    RUN->>PG: ExecInTx (set_config local timeouts + SQL + INSERT tracking + COMMIT)
                else Directives.Transaction == false
                    RUN->>MIG: SplitStatements(RawSQL)
                    RUN->>PG: ExecNoTx (set_config session timeouts + exec tiap statement + RESET + InsertRecord)
                end
            end
            RUN->>PG: UnlockAdvisory & Close
            RUN-->>CLI: Return UpResult
        end
    end
```

## Prinsip Desain Utama

1. **Fail-fast sebelum menyentuh database**: Pada `runner.Up`, seluruh file lokal dimuat dan di-lint terlebih dahulu sebelum `connect(ctx)` dipanggil. Error sintaks atau linter tidak pernah membuka koneksi ataupun mengambil lock di PostgreSQL.
2. **Satu koneksi tunggal untuk session state**: `PostgresStore` tidak menggunakan connection pool (`pgxpool`), melainkan `pgx.Connect` tunggal per database, karena `pg_try_advisory_lock` dan `SET SESSION` terikat pada satu koneksi TCP PostgreSQL.
3. **Pure-stdlib migration parser**: Paket `internal/migration` bebas dari dependensi driver DB maupun I/O jaringan sehingga seluruh logika parsing, checksum, splitting, dan linting mudah diuji deterministik.
