# `database-migrator-service` docs

## Using `database-migrator-service`

Baru mengenal `database-migrator-service`? Mulai dari **[User Guide & Index](./user/README.md)** untuk memahami cara kerja sistem, prasyarat, konsep inti, dan daftar lengkap panduan pengguna.

### Essentials

- [Quickstart lokal](./user/quickstart.md) · [Konfigurasi `databases.yaml`](./user/configuration.md) · [Referensi CLI (`pg-migrate`)](./user/cli.md) · [FAQ](./user/faq.md)

### Workflow & Safety

- [Header directive & SQL safety linter](./user/directives-and-linter.md) · [SOP branching & out-of-order execution](./user/branching-workflow.md)

### CI/CD & Integration

- [Integrasi repo backend Jenkins](./user/jenkins-integration.md) · [Adopsi database existing (Baseline)](./operations/01_existing-db-adoption.md)

---

## Working on `database-migrator-service`

Mulai dari [development runbook](./operations/00_development.md).

Dokumentasi internal mendokumentasikan keputusan arsitektur, batasan transaksi/lock, aturan parser & linter, serta perilaku runner yang tidak selalu langsung terlihat dari potongan kode terpisah.

### Internals (in reading order)

- [00 · Glossary](./internals/00_glossary.md)
- [01 · Architecture](./internals/01_architecture.md)
- [02 · Configuration & DSN Resolution](./internals/02_config-and-dsn.md)
- [03 · SQL Parser & Safety Linter](./internals/03_parser-and-linter.md)
- [04 · Execution Runner & Advisory Locking](./internals/04_runner-and-locking.md)
- [05 · Testing](./internals/05_testing.md)

### Runbooks

- [00 · Development and local builds](./operations/00_development.md)
- [01 · Adopting existing databases (Baseline)](./operations/01_existing-db-adoption.md)
- [02 · Jenkins CI/CD setup](./operations/02_jenkins-setup.md)
- [03 · Troubleshooting & incident recovery](./operations/03_troubleshooting.md)
