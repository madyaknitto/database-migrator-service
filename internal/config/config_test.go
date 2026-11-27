package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "databases.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("gagal menulis temp config: %v", err)
	}
	return path
}

func mapEnv(values map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := values[k]
		return v, ok
	}
}

func TestLoadDefaultsAndEnabledDefaultTrue(t *testing.T) {
	cfgPath := writeTempConfig(t, `
databases:
  - name: db_name
    database: db_name
    dir: databases/db_name
  - name: legacy_db
    database: legacy_db
    dir: databases/legacy_db
    enabled: false
`)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load gagal tak terduga: %v", err)
	}

	if cfg.Defaults.TrackingSchema != DefaultTrackingSchema {
		t.Errorf("TrackingSchema = %q, ingin %q", cfg.Defaults.TrackingSchema, DefaultTrackingSchema)
	}
	if cfg.Defaults.TrackingTable != DefaultTrackingTable {
		t.Errorf("TrackingTable = %q, ingin %q", cfg.Defaults.TrackingTable, DefaultTrackingTable)
	}
	if cfg.Defaults.LockTimeout != DefaultLockTimeout {
		t.Errorf("LockTimeout = %q, ingin %q", cfg.Defaults.LockTimeout, DefaultLockTimeout)
	}
	if cfg.Defaults.StatementTimeout != DefaultStmtTimeout {
		t.Errorf("StatementTimeout = %q, ingin %q", cfg.Defaults.StatementTimeout, DefaultStmtTimeout)
	}
	if cfg.Defaults.LockWaitTimeout != DefaultLockWaitTimeout {
		t.Errorf("LockWaitTimeout = %q, ingin %q", cfg.Defaults.LockWaitTimeout, DefaultLockWaitTimeout)
	}

	if !cfg.Databases[0].Enabled {
		t.Errorf("db_name seharusnya default enabled = true")
	}
	if cfg.Databases[1].Enabled {
		t.Errorf("legacy_db seharusnya enabled = false")
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "invalid tracking_schema",
			yaml: `
defaults:
  tracking_schema: "public; DROP TABLE x"
databases:
  - name: db_name
    database: db_name
    dir: databases/db_name
`,
			wantErr: "tracking_schema",
		},
		{
			name: "invalid tracking_table",
			yaml: `
defaults:
  tracking_table: "123_bad"
databases:
  - name: db_name
    database: db_name
    dir: databases/db_name
`,
			wantErr: "tracking_table",
		},
		{
			name: "invalid duration",
			yaml: `
defaults:
  lock_timeout: "-5s"
databases:
  - name: db_name
    database: db_name
    dir: databases/db_name
`,
			wantErr: "lock_timeout",
		},
		{
			name: "duplicate database name",
			yaml: `
databases:
  - name: db_name
    database: db_name
    dir: databases/db_name
  - name: db_name
    database: db_name_2
    dir: databases/db_name_2
`,
			wantErr: "duplikat",
		},
		{
			name: "reserved name all",
			yaml: `
databases:
  - name: all
    database: all_db
    dir: databases/all
`,
			wantErr: "all",
		},
		{
			name: "missing dir",
			yaml: `
databases:
  - name: db_name
    database: db_name
`,
			wantErr: "dir",
		},
		{
			name: "missing database and dsn_env",
			yaml: `
databases:
  - name: db_name
    dir: databases/db_name
`,
			wantErr: "dsn_env",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := writeTempConfig(t, tc.yaml)
			_, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("diharapkan error mengandung %q, tetapi nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q tidak mengandung %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSelectDatabases(t *testing.T) {
	cfg := &Config{
		Databases: []DatabaseConfig{
			{Name: "db_name", Database: "db_name", Dir: "databases/db_name", Enabled: true},
			{Name: "analytics_db", Database: "analytics_db", Dir: "databases/analytics_db", Enabled: true},
			{Name: "disabled_db", Database: "disabled_db", Dir: "databases/disabled_db", Enabled: false},
		},
	}

	// Select all (default)
	all, err := cfg.SelectDatabases("all")
	if err != nil {
		t.Fatalf("SelectDatabases(all) gagal: %v", err)
	}
	if len(all) != 2 || all[0].Name != "db_name" || all[1].Name != "analytics_db" {
		t.Fatalf("hasil all tidak sesuai: %+v", all)
	}

	// Select empty string -> all
	emptySel, err := cfg.SelectDatabases("")
	if err != nil || len(emptySel) != 2 {
		t.Fatalf("SelectDatabases('') tidak sesuai: len=%d err=%v", len(emptySel), err)
	}

	// Single
	single, err := cfg.SelectDatabases("analytics_db")
	if err != nil || len(single) != 1 || single[0].Name != "analytics_db" {
		t.Fatalf("SelectDatabases(analytics_db) gagal: %+v err=%v", single, err)
	}

	// Multi + duplicate + spaces
	multi, err := cfg.SelectDatabases(" db_name , analytics_db , db_name ")
	if err != nil || len(multi) != 2 {
		t.Fatalf("SelectDatabases multi gagal: %+v err=%v", multi, err)
	}

	// Unknown
	if _, err := cfg.SelectDatabases("unknown_db"); err == nil {
		t.Fatal("diharapkan error untuk database tidak terdaftar")
	}

	// Disabled
	if _, err := cfg.SelectDatabases("disabled_db"); err == nil {
		t.Fatal("diharapkan error untuk database berstatus disabled")
	}
}

func TestResolveDSN(t *testing.T) {
	dbPG := DatabaseConfig{
		Name:        "db_name",
		Database:    "db_name",
		DatabaseEnv: "POSTGRES_DB_NAME",
		DSNEnv:      "POSTGRES_DSN_DB_NAME",
	}

	t.Run("POSTGRES_* takes priority over PG_* and DB_*", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"POSTGRES_HOST":     "pg-primary.internal",
			"POSTGRES_PORT":     "5434",
			"POSTGRES_USER":     "pg_migrator",
			"POSTGRES_PASSWORD": "pg_secret",
			"POSTGRES_SSLMODE":  "verify-full",
			"PG_HOST":           "pg-legacy.internal",
			"PG_USER":           "legacy_user",
			"DB_HOST":           "generic.internal",
			"DB_USER":           "generic_user",
		})
		dsn, actual, err := ResolveDSN(dbPG, env)
		if err != nil {
			t.Fatalf("ResolveDSN POSTGRES_* gagal: %v", err)
		}
		if actual != "db_name" {
			t.Errorf("actualDBName = %q, ingin db_name", actual)
		}
		want := "postgres://pg_migrator:pg_secret@pg-primary.internal:5434/db_name?sslmode=verify-full"
		if dsn != want {
			t.Errorf("dsn = %q, ingin %q", dsn, want)
		}
	})

	t.Run("shared server default db name and special password chars", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"PG_HOST":     "10.0.0.5",
			"PG_PORT":     "5433",
			"PG_USER":     "migrator_user",
			"PG_PASSWORD": "p@ss:w/ord#123",
			"PG_SSLMODE":  "require",
		})
		dsn, actual, err := ResolveDSN(dbPG, env)
		if err != nil {
			t.Fatalf("ResolveDSN gagal: %v", err)
		}
		if actual != "db_name" {
			t.Errorf("actualDBName = %q, ingin db_name", actual)
		}
		want := "postgres://migrator_user:p%40ss%3Aw%2Ford%23123@10.0.0.5:5433/db_name?sslmode=require"
		if dsn != want {
			t.Errorf("dsn = %q, ingin %q", dsn, want)
		}
	})

	t.Run("fallback to DB_* env vars", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"DB_HOST":     "pg.internal",
			"DB_PORT":     "5432",
			"DB_USER":     "migrator_user",
			"DB_PASSWORD": "secret",
			"DB_SSLMODE":  "verify-full",
		})
		dsn, actual, err := ResolveDSN(dbPG, env)
		if err != nil {
			t.Fatalf("ResolveDSN fallback DB_* gagal: %v", err)
		}
		if actual != "db_name" {
			t.Errorf("actualDBName = %q, ingin db_name", actual)
		}
		if !strings.Contains(dsn, "@pg.internal:5432/db_name?sslmode=verify-full") {
			t.Errorf("dsn tidak sesuai fallback DB_*: %q", dsn)
		}
	})

	t.Run("database_env override", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"POSTGRES_HOST":     "localhost",
			"POSTGRES_USER":     "migrator_user",
			"POSTGRES_PASSWORD": "secret",
			"POSTGRES_DB_NAME":  "db_name_staging",
		})
		dsn, actual, err := ResolveDSN(dbPG, env)
		if err != nil {
			t.Fatalf("ResolveDSN gagal: %v", err)
		}
		if actual != "db_name_staging" {
			t.Errorf("actualDBName = %q, ingin db_name_staging", actual)
		}
		if !strings.Contains(dsn, "/db_name_staging?sslmode=disable") {
			t.Errorf("dsn tidak memuat override database_env: %q", dsn)
		}
	})

	t.Run("dsn_env override", func(t *testing.T) {
		env := mapEnv(map[string]string{
			"POSTGRES_DSN_DB_NAME": "postgres://custom:pw@other-pg:5432/custom_db?sslmode=verify-full",
		})
		dsn, actual, err := ResolveDSN(dbPG, env)
		if err != nil {
			t.Fatalf("ResolveDSN gagal: %v", err)
		}
		if dsn != "postgres://custom:pw@other-pg:5432/custom_db?sslmode=verify-full" {
			t.Errorf("dsn tidak sesuai override: %q", dsn)
		}
		if actual != "custom_db" {
			t.Errorf("actualDBName = %q, ingin custom_db", actual)
		}
	})

	t.Run("missing host or user", func(t *testing.T) {
		_, _, err := ResolveDSN(dbPG, mapEnv(map[string]string{"POSTGRES_USER": "migrator"}))
		if err == nil || !strings.Contains(err.Error(), "POSTGRES_HOST") {
			t.Fatalf("diharapkan error POSTGRES_HOST, dapat: %v", err)
		}
		_, _, err = ResolveDSN(dbPG, mapEnv(map[string]string{"POSTGRES_HOST": "localhost"}))
		if err == nil || !strings.Contains(err.Error(), "POSTGRES_USER") {
			t.Fatalf("diharapkan error POSTGRES_USER, dapat: %v", err)
		}
	})
}
