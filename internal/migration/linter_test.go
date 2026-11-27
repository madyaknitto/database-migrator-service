package migration

import (
	"testing"
)

func makeTestFile(t *testing.T, sql string, isBaseline bool) File {
	t.Helper()
	norm := NormalizeLineEndings(sql)
	dirs, err := ParseDirectives(norm)
	if err != nil {
		t.Fatalf("ParseDirectives gagal: %v", err)
	}
	return File{
		Version:    "20261126120000",
		Filename:   "20261126120000_order_test.sql",
		Service:    "order",
		IsBaseline: isBaseline,
		RawSQL:     norm,
		Checksum:   ComputeChecksum(norm),
		Directives: dirs,
	}
}

func hasRule(issues []Issue, rule, severity string) bool {
	for _, iss := range issues {
		if iss.Rule == rule && (severity == "" || iss.Severity == severity) {
			return true
		}
	}
	return false
}

func TestLintFile_CommentsAndStringsIgnored(t *testing.T) {
	f := makeTestFile(t, `
-- Jangan pernah DROP TABLE users; di produksi!
/* TRUNCATE orders; DROP COLUMN id; */
INSERT INTO audit_logs (note) VALUES ('DROP TABLE users; ALTER TABLE x RENAME TO y;');
`, false)

	issues := LintFile(f)
	if len(issues) != 0 {
		t.Fatalf("seharusnya 0 issue untuk kata kunci di dalam komentar/string, dapat: %+v", issues)
	}
}

func TestLintFile_ConcurrentIndexRules(t *testing.T) {
	t.Run("CONCURRENTLY with default transaction=true -> ERROR", func(t *testing.T) {
		f := makeTestFile(t, `
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_user ON orders (user_id);
`, false)
		issues := LintFile(f)
		if !hasRule(issues, "NO_TX_REQUIRED", SeverityError) {
			t.Fatalf("diharapkan ERROR NO_TX_REQUIRED, dapat: %+v", issues)
		}
	})

	t.Run("CONCURRENTLY with transaction=false and IF NOT EXISTS -> clean", func(t *testing.T) {
		f := makeTestFile(t, `
-- migrate:transaction false
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_user ON orders (user_id);
`, false)
		issues := LintFile(f)
		if len(issues) != 0 {
			t.Fatalf("diharapkan 0 issue, dapat: %+v", issues)
		}
	})

	t.Run("CONCURRENTLY without IF NOT EXISTS -> WARNING", func(t *testing.T) {
		f := makeTestFile(t, `
-- migrate:transaction false
CREATE INDEX CONCURRENTLY idx_orders_user ON orders (user_id);
`, false)
		issues := LintFile(f)
		if !hasRule(issues, "CONCURRENT_WITHOUT_IF_NOT_EXISTS", SeverityWarning) {
			t.Fatalf("diharapkan WARNING CONCURRENT_WITHOUT_IF_NOT_EXISTS, dapat: %+v", issues)
		}
	})
}

func TestLintFile_BlockingIndexNewVsExistingTable(t *testing.T) {
	t.Run("CREATE INDEX on newly created table in same file -> clean", func(t *testing.T) {
		f := makeTestFile(t, `
CREATE TABLE public.vouchers (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL
);
CREATE INDEX idx_vouchers_code ON vouchers (code);
`, false)
		issues := LintFile(f)
		if len(issues) != 0 {
			t.Fatalf("index pada tabel baru di file yang sama seharusnya aman, dapat: %+v", issues)
		}
	})

	t.Run("CREATE INDEX on existing table without CONCURRENTLY -> WARNING", func(t *testing.T) {
		f := makeTestFile(t, `
CREATE INDEX idx_orders_status ON public.orders (status);
`, false)
		issues := LintFile(f)
		if !hasRule(issues, "BLOCKING_INDEX", SeverityWarning) {
			t.Fatalf("diharapkan WARNING BLOCKING_INDEX, dapat: %+v", issues)
		}
	})
}

func TestLintFile_DropColumnVsDropDefaultAndNotNull(t *testing.T) {
	t.Run("DROP DEFAULT and DROP NOT NULL and DROP CONSTRAINT -> clean", func(t *testing.T) {
		f := makeTestFile(t, `
ALTER TABLE orders ALTER COLUMN discount DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN note DROP NOT NULL;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS chk_orders_positive;
`, false)
		issues := LintFile(f)
		if len(issues) != 0 {
			t.Fatalf("DROP DEFAULT / NOT NULL / CONSTRAINT seharusnya bukan DROP COLUMN, dapat: %+v", issues)
		}
	})

	t.Run("DROP COLUMN explicit -> ERROR", func(t *testing.T) {
		f := makeTestFile(t, `
ALTER TABLE orders DROP COLUMN legacy_code;
`, false)
		issues := LintFile(f)
		if !hasRule(issues, "DESTRUCTIVE_DROP_COLUMN", SeverityError) {
			t.Fatalf("diharapkan ERROR DESTRUCTIVE_DROP_COLUMN, dapat: %+v", issues)
		}
	})

	t.Run("DROP column shorthand without COLUMN keyword -> ERROR", func(t *testing.T) {
		f := makeTestFile(t, `
ALTER TABLE orders DROP IF EXISTS legacy_code;
`, false)
		issues := LintFile(f)
		if !hasRule(issues, "DESTRUCTIVE_DROP_COLUMN", SeverityError) {
			t.Fatalf("diharapkan ERROR DESTRUCTIVE_DROP_COLUMN untuk shorthand, dapat: %+v", issues)
		}
	})

	t.Run("DROP COLUMN allowed with allow-destructive -> clean", func(t *testing.T) {
		f := makeTestFile(t, `
-- migrate:allow-destructive semua service backend sudah pindah ke kolom baru
ALTER TABLE orders DROP COLUMN IF EXISTS legacy_code;
`, false)
		issues := LintFile(f)
		if len(issues) != 0 {
			t.Fatalf("diharapkan 0 issue saat allow-destructive diisi, dapat: %+v", issues)
		}
	})
}

func TestLintFile_AddColumnNotNullWithoutDefault(t *testing.T) {
	t.Run("NOT NULL without DEFAULT -> WARNING", func(t *testing.T) {
		f := makeTestFile(t, `
ALTER TABLE users ADD COLUMN role TEXT NOT NULL;
`, false)
		issues := LintFile(f)
		if !hasRule(issues, "NOT_NULL_WITHOUT_DEFAULT", SeverityWarning) {
			t.Fatalf("diharapkan WARNING NOT_NULL_WITHOUT_DEFAULT, dapat: %+v", issues)
		}
	})

	t.Run("NOT NULL with DEFAULT -> clean", func(t *testing.T) {
		f := makeTestFile(t, `
ALTER TABLE users ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT true;
`, false)
		issues := LintFile(f)
		if len(issues) != 0 {
			t.Fatalf("diharapkan 0 issue jika ada DEFAULT, dapat: %+v", issues)
		}
	})
}

func TestLintFile_BaselineSkipsDestructiveChecks(t *testing.T) {
	f := makeTestFile(t, `
DROP TABLE IF EXISTS tmp_old;
CREATE INDEX idx_users_email ON users (email);
`, true)
	issues := LintFile(f)
	if len(issues) != 0 {
		t.Fatalf("file baseline seharusnya melewati aturan destructive/blocking, dapat: %+v", issues)
	}
}
