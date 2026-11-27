package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumNormalizedLineEndings(t *testing.T) {
	lf := "-- migrate:service order\nSELECT 1;\n"
	crlf := "-- migrate:service order\r\nSELECT 1;\r\n"

	c1 := ComputeChecksum(NormalizeLineEndings(lf))
	c2 := ComputeChecksum(NormalizeLineEndings(crlf))
	if c1 != c2 {
		t.Fatalf("checksum LF (%s) dan CRLF (%s) seharusnya identik", c1, c2)
	}
}

func TestParseDirectives(t *testing.T) {
	sql := `
-- Komentar umum
-- migrate:service order-service
-- migrate:ticket PROJ-101
-- migrate:transaction false
-- migrate:lock-timeout 10s
-- migrate:statement-timeout 5m
-- migrate:allow-destructive hapus kolom legacy setelah v2 rilis
-- migrate:allow-blocking-index tabel kecil dev-only

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_user ON orders (user_id);
`
	dirs, err := ParseDirectives(NormalizeLineEndings(sql))
	if err != nil {
		t.Fatalf("ParseDirectives gagal: %v", err)
	}

	if dirs.Service != "order-service" {
		t.Errorf("Service = %q", dirs.Service)
	}
	if dirs.Ticket != "PROJ-101" {
		t.Errorf("Ticket = %q", dirs.Ticket)
	}
	if dirs.Transaction {
		t.Errorf("Transaction seharusnya false")
	}
	if dirs.LockTimeout != "10s" {
		t.Errorf("LockTimeout = %q", dirs.LockTimeout)
	}
	if dirs.StatementTimeout != "5m" {
		t.Errorf("StatementTimeout = %q", dirs.StatementTimeout)
	}
	if dirs.AllowDestructive == "" {
		t.Errorf("AllowDestructive seharusnya terisi")
	}
	if dirs.AllowBlockingIndex == "" {
		t.Errorf("AllowBlockingIndex seharusnya terisi")
	}
}

func TestParseDirectivesErrors(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"unknown directive", "-- migrate:foo bar\nSELECT 1;"},
		{"invalid transaction", "-- migrate:transaction yes\nSELECT 1;"},
		{"invalid lock-timeout", "-- migrate:lock-timeout -1s\nSELECT 1;"},
		{"allow-destructive without reason", "-- migrate:allow-destructive\nDROP TABLE x;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseDirectives(tc.sql); err == nil {
				t.Fatalf("diharapkan error untuk kasus %s", tc.name)
			}
		})
	}
}

func TestSplitStatementsPostgreSQL(t *testing.T) {
	sql := `
-- komentar awal; bukan statement
/* nested /* block; comment */ masih komentar */
CREATE TABLE items (id INT PRIMARY KEY, note TEXT DEFAULT 'a;b''c');

DO $$
BEGIN
    IF TRUE THEN
        RAISE NOTICE 'semicolon ; inside dollar quote';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION fn_test() RETURNS TEXT AS $fn$
BEGIN
    RETURN 'x;y';
END;
$fn$ LANGUAGE plpgsql;

/* hanya komentar penutup */
`
	stmts := SplitStatements(sql)
	if len(stmts) != 3 {
		t.Fatalf("jumlah statement = %d, ingin 3; got=%#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "CREATE TABLE items") {
		t.Errorf("statement[0] salah: %q", stmts[0])
	}
	if !strings.Contains(stmts[1], "DO $$") || !strings.Contains(stmts[1], "END $$") {
		t.Errorf("statement[1] dollar-quote terpecah: %q", stmts[1])
	}
	if !strings.Contains(stmts[2], "$fn$") {
		t.Errorf("statement[2] tagged dollar-quote salah: %q", stmts[2])
	}
}

func TestLoadDatabaseFiles(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "db_name")
	if err := os.MkdirAll(filepath.Join(baseDir, "baseline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	_ = os.WriteFile(filepath.Join(baseDir, "migrations", ".gitkeep"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(baseDir, "baseline", "00000000000000_initial_baseline.sql"), []byte("SELECT 1;"), 0o644)
	_ = os.WriteFile(filepath.Join(baseDir, "migrations", "20261126140000_auth_add_sessions.sql"), []byte("SELECT 2;"), 0o644)
	_ = os.WriteFile(filepath.Join(baseDir, "migrations", "20261126090000_order_add_col.sql"), []byte("SELECT 1;"), 0o644)

	baseline, migrations, err := LoadDatabaseFiles(baseDir)
	if err != nil {
		t.Fatalf("LoadDatabaseFiles gagal: %v", err)
	}
	if baseline == nil || baseline.Version != "00000000000000" {
		t.Fatalf("baseline tidak sesuai: %+v", baseline)
	}
	if len(migrations) != 2 {
		t.Fatalf("jumlah migrations = %d, ingin 2", len(migrations))
	}
	if migrations[0].Version != "20261126090000" || migrations[1].Version != "20261126140000" {
		t.Fatalf("urutan migrasi salah: %s, %s", migrations[0].Version, migrations[1].Version)
	}
	if migrations[0].Service != "order" {
		t.Errorf("service = %q, ingin 'order'", migrations[0].Service)
	}
}

func TestLoadDatabaseFilesRejectsInvalidOrDuplicate(t *testing.T) {
	t.Run("duplicate version", func(t *testing.T) {
		baseDir := t.TempDir()
		migDir := filepath.Join(baseDir, "migrations")
		_ = os.MkdirAll(migDir, 0o755)
		_ = os.WriteFile(filepath.Join(migDir, "20261126090000_a_first.sql"), []byte("SELECT 1;"), 0o644)
		_ = os.WriteFile(filepath.Join(migDir, "20261126090000_b_second.sql"), []byte("SELECT 2;"), 0o644)

		if _, _, err := LoadDatabaseFiles(baseDir); err == nil {
			t.Fatal("diharapkan error untuk versi duplikat")
		}
	})

	t.Run("zero version in migrations dir", func(t *testing.T) {
		baseDir := t.TempDir()
		migDir := filepath.Join(baseDir, "migrations")
		_ = os.MkdirAll(migDir, 0o755)
		_ = os.WriteFile(filepath.Join(migDir, "00000000000000_a_bad.sql"), []byte("SELECT 1;"), 0o644)

		if _, _, err := LoadDatabaseFiles(baseDir); err == nil {
			t.Fatal("diharapkan error untuk versi 00000000000000 di migrations/")
		}
	})
}
