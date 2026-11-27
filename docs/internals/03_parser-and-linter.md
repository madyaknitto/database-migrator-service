# SQL Parser & Safety Linter

Paket [`internal/migration`](../../internal/migration/parser.go) menangani pemuatan file dari disk, normalisasi checksum, parsing header directive, pemecahan statement PostgreSQL, serta analisis keamanan statis.

## Pemuatan File & Normalisasi Checksum (`parser.go`)

1. **`LoadDatabaseFiles(baseDir)`**:
   - Memindai `<baseDir>/baseline/` (jika ada): mengabaikan file bantu (`isIgnorableAuxFile`: `.gitkeep`, `readme.md`, `readme.txt`, `.ds_store`), menolak file non-`.sql`, dan memastikan hanya ada **maksimal 1 file** baseline yang cocok dengan `BaselineFileRegex`.
   - Memindai `<baseDir>/migrations/` (jika ada): menolak versi `00000000000000`, menolak versi duplikat antar file, lalu mengurutkan slice `migrations` secara ascending berdasarkan `Version` (tie-breaker `Filename`).
2. **Checksum Deterministik**:
   - `NormalizeLineEndings` mengubah `\r\n` dan `\r` menjadi `\n` sebelum parsing directive dan komputasi `ComputeChecksum` (`sha256.Sum256`). Ini mencegah false `CHECKSUM_MISMATCH` saat file di-commit dari Windows dan dieksekusi di container Linux Jenkins.

## Header Directive Parser (`ParseDirectives`)

`ParseDirectives` memindai baris dari atas dengan aturan:
- Baris kosong, komentar `--` biasa, dan blok komentar `/* ... */` (termasuk multiline) dilewati.
- Jika setelah `*/` di baris yang sama terdapat teks bukan komentar `--`, header selesai dan loop berhenti (`break`).
- Pada komentar `-- migrate:<key> <value>`, key dikenali *case-insensitive*: `service`, `ticket`, `transaction` (`true`/`false`), `lock-timeout` & `statement-timeout` (divalidasi `time.ParseDuration` > 0), `allow-destructive`, dan `allow-blocking-index` (dua terakhir wajib menyertakan alasan).
- Directive tidak dikenal langsung mengembalikan error agar salah ketik tidak terabaikan diam-diam.

## Tokenizer PostgreSQL (`SplitStatements` & `StripCommentsAndStrings`)

Kedua fungsi menggunakan state machine berkarakter tunggal yang mengenali konteks leksikal PostgreSQL:
- Line comment `-- ... \n`
- **Nested block comment** `/* ... /* ... */ ... */` menggunakan penghitung `depth` (sesuai standar PostgreSQL).
- Escape string `E'...'` / `e'...'` (hanya jika `E` tidak didahului karakter identifier, mendukung `\'` dan `''`).
- Single-quoted string `'...'` (mendukung `''`).
- Double-quoted identifier `"..."` (mendukung `""`).
- **Dollar-quoted string** `$$...$$` dan tagged `$tag$...$tag$` (melalui `parseDollarTag`), sehingga titik koma `;` maupun kata kunci di dalam fungsi `PL/pgSQL` / blok `DO $$ ... $$` tidak memecah statement atau memicu linter.

Pada `StripCommentsAndStrings`, komentar diganti spasi, literal string (`'...'`, `E'...'`, `$$...$$`) diganti placeholder `''`, sedangkan double-quoted identifier `"..."` dipertahankan agar regex `CREATE TABLE` / `CREATE INDEX` tetap mengenali nama tabel yang di-quote. `hasMeaningfulSQL` memanfaatkan fungsi ini sehingga file/potongan yang hanya berisi komentar tidak dianggap statement SQL.

## Implementasi Safety Linter ([`linter.go`](../../internal/migration/linter.go))

`LintFile(f)` terlebih dahulu memecah file dengan `SplitStatements(f.RawSQL)`:
- Jika jumlah statement = 0 → mengembalikan `EMPTY_SQL` (`ERROR`).
- Jika `f.IsBaseline` bernilai `true` → berhenti di sini (file dump `pg_dump` dilewatkan dari aturan destruktif/blocking).
- Pass pertama mengumpulkan semua nama tabel yang dibuat di file yang sama (`createdTables`) melalui `reCreateTable` dan menormalisasinya dengan `normalizeTableVariants` (`schema.table`, unqualified `table`, dan `public.table` dalam huruf kecil tanpa kutip/spasi).
- Pass kedua memeriksa tiap statement yang sudah dibersihkan komentar & string-nya:
  1. **`NO_TX_REQUIRED` (`ERROR`)**: `f.Directives.Transaction == true` sementara statement mengandung `CREATE/DROP INDEX CONCURRENTLY`, `REINDEX ... CONCURRENTLY`, atau `VACUUM`.
  2. **DDL Destruktif** (aktif ketika `f.Directives.AllowDestructive == ""`):
     - `DESTRUCTIVE_DROP_TABLE` (`ERROR`), `DESTRUCTIVE_TRUNCATE` (`ERROR`), `BREAKING_RENAME` (`ERROR` untuk `ALTER TABLE ... RENAME`).
     - `DESTRUCTIVE_DROP_COLUMN` (`ERROR`) via `hasDropColumn`: mendeteksi `DROP COLUMN` maupun shorthand `ALTER TABLE t DROP [IF EXISTS] col`, namun mengecualikan `CONSTRAINT`, `DEFAULT`, `NOT` (`DROP NOT NULL`), `IDENTITY`, dan `EXPRESSION`.
  3. **`BLOCKING_INDEX` (`WARNING`)** (aktif ketika `f.Directives.AllowBlockingIndex == ""`): `CREATE [UNIQUE] INDEX` tanpa `CONCURRENTLY` pada tabel yang tidak ada di `createdTables`.
  4. **`NOT_NULL_WITHOUT_DEFAULT` (`WARNING`)** via `hasAddColumnNotNullWithoutDefault`: memecah klausa `ALTER TABLE` dengan `splitTopLevelComma` (hanya koma di kedalaman kurung `0`), mengabaikan `ADD PRIMARY/UNIQUE/CHECK/FOREIGN/CONSTRAINT`, lalu melapor jika ada `NOT NULL` tanpa `DEFAULT`.
  5. **`CONCURRENT_WITHOUT_IF_NOT_EXISTS` (`WARNING`)**: `CREATE [UNIQUE] INDEX CONCURRENTLY` tanpa `IF NOT EXISTS`.
