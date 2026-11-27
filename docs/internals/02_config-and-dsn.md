# Configuration & DSN Resolution

Paket [`internal/config`](../../internal/config/config.go) bertanggung jawab memuat [`databases.yaml`](../../databases.yaml), menerapkan nilai default, memvalidasi konfigurasi, menyeleksi target database, dan membentuk DSN PostgreSQL.

## Konstanta Default & Validasi

| Konstanta | Nilai | Dipakai Saat |
|---|---|---|
| `DefaultTrackingSchema` | `"public"` | `defaults.tracking_schema` kosong |
| `DefaultTrackingTable` | `"schema_migrations"` | `defaults.tracking_table` kosong |
| `DefaultLockTimeout` | `"5s"` | `defaults.lock_timeout` kosong |
| `DefaultStmtTimeout` | `"60s"` | `defaults.statement_timeout` kosong |
| `DefaultLockWaitTimeout` | `"120s"` | `defaults.lock_wait_timeout` kosong |

Aturan validasi pada `Config.validate()`:
- `tracking_schema` dan `tracking_table` wajib cocok dengan `identRegex` (`^[a-zA-Z_][a-zA-Z0-9_]*$`) untuk mencegah identifier SQL tidak valid sebelum masuk ke query.
- `lock_timeout`, `statement_timeout`, dan `lock_wait_timeout` harus valid menurut `time.ParseDuration` dan bernilai `> 0`.
- `databases` tidak boleh kosong. Setiap entri wajib memiliki:
  - `name` unik, bukan `"all"` (*case-insensitive*), cocok dengan `dbNameRegex` (`^[a-zA-Z0-9_-]+$`).
  - `dir` non-kosong.
  - Minimal salah satu dari `database` atau `dsn_env` terisi.

## Perilaku `UnmarshalYAML` pada `DatabaseConfig`

Field `enabled` pada YAML menggunakan tipe pointer `*bool` di struct internal `rawDatabaseConfig`. Jika field `enabled` tidak ditulis di `databases.yaml`, `UnmarshalYAML` otomatis mengaturnya menjadi `true`, sehingga entri baru aktif secara bawaan tanpa perlu menulis `enabled: true` berulang-ulang.

## Seleksi Database (`SelectDatabases`)

- Selector `""` atau `"all"` (*case-insensitive*) memilih semua entri dengan `Enabled == true`; gagal jika tidak ada satu pun yang aktif.
- Selector berupa nama atau daftar dipisah koma (mis. `"db_name,billing_db"`):
  - Memangkas spasi di tiap elemen, menolak entri kosong dan penggabungan `"all"` dengan nama lain.
  - Melakukan deduplikasi sambil mempertahankan urutan input.
  - Gagal jika database tidak terdaftar atau berstatus `enabled: false`.

## Resolusi Path & DSN (`ResolveDir`, `ResolveDSN`, `ResolveAll`)

- **`ResolveDir`**: Jika `db.Dir` relatif, path digabungkan terhadap `Config.BaseDir` (direktori induk dari file config yang di-load via `filepath.Abs`), bukan working directory proses yang memanggil.
- **`ResolveDSN(db, lookupEnv)`**:
  1. Jika `db.DSNEnv` diset dan ada di environment dengan nilai non-kosong, full DSN dikembalikan apa adanya dan nama database aktual diekstrak dari `url.Parse(rawDSN).Path` (fallback ke `db.Database`, lalu `db.Name`).
  2. Jika tidak, `actualDBName` diambil dari `db.Database` atau ditimpa oleh env `db.DatabaseEnv` bila tersedia.
  3. Variabel koneksi server bersama dicari dengan urutan prioritas:
     - Host (wajib): `POSTGRES_HOST` → `PG_HOST` → `DB_HOST`
     - User (wajib): `POSTGRES_USER` → `PG_USER` → `DB_USER`
     - Port: `POSTGRES_PORT` → `PG_PORT` → `DB_PORT` (default `"5432"`, validasi integer `1`–`65535`)
     - Password: `POSTGRES_PASSWORD` → `PG_PASSWORD` → `DB_PASSWORD` (memakai `firstPresentEnv` agar karakter spesial seperti `@`, `:`, `/`, `#` di-escape aman melalui `url.UserPassword`)
     - SSL Mode: `POSTGRES_SSLMODE` → `PG_SSLMODE` → `DB_SSLMODE` (default `"disable"`)
  4. Hasil akhir disusun menggunakan `&url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, port), ...}` sehingga aman untuk alamat IPv6 maupun karakter khusus.
