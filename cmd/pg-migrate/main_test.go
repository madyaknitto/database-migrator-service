package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database-migrator-service/internal/migration"
	"database-migrator-service/internal/runner"
)

func setupValidateTestWorkspace(t *testing.T, migrationFilename, migrationSQL string) string {
	t.Helper()
	tmpDir := t.TempDir()

	dbDir := filepath.Join(tmpDir, "databases", "db_name")
	baseDir := filepath.Join(dbDir, "baseline")
	migDir := filepath.Join(dbDir, "migrations")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("mkdir baseline: %v", err)
	}
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations: %v", err)
	}

	baselineSQL := "-- migrate:service baseline\nCREATE TABLE IF NOT EXISTS public.orders (id BIGSERIAL PRIMARY KEY);\n"
	if err := os.WriteFile(filepath.Join(baseDir, "00000000000000_initial_baseline.sql"), []byte(baselineSQL), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migDir, migrationFilename), []byte(migrationSQL), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	cfgYAML := `version: 1
defaults:
  tracking_schema: public
  tracking_table: schema_migrations
  lock_timeout: 5s
  statement_timeout: 60s
  lock_wait_timeout: 120s
databases:
  - name: db_name
    database: db_name
    dir: databases/db_name
    enabled: true
`
	cfgPath := filepath.Join(tmpDir, "databases.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write databases.yaml: %v", err)
	}
	return cfgPath
}

func TestRunValidateJSON_ValidSQL(t *testing.T) {
	cfgPath := setupValidateTestWorkspace(
		t,
		"20261127090000_order_add_discount.sql",
		"-- migrate:service order\n-- migrate:transaction true\nALTER TABLE public.orders ADD COLUMN IF NOT EXISTS discount NUMERIC(15,2) NOT NULL DEFAULT 0;\n",
	)

	var buf bytes.Buffer
	err := runValidate([]string{"--config=" + cfgPath, "--db=all", "--json"}, &buf)
	if err != nil {
		t.Fatalf("runValidate failed: %v\noutput: %s", err, buf.String())
	}

	raw := buf.String()
	if !strings.Contains(raw, `"issues": []`) {
		t.Fatalf("expected empty issues array [] instead of null, got:\n%s", raw)
	}

	var report validateReportJSON
	if uErr := json.Unmarshal(buf.Bytes(), &report); uErr != nil {
		t.Fatalf("invalid json output: %v\n%s", uErr, raw)
	}

	if !report.Passed || report.ErrorCount != 0 || report.WarningCount != 0 {
		t.Fatalf("unexpected report summary: %+v", report)
	}
	if len(report.Databases) != 1 || !report.Databases[0].BaselinePresent || report.Databases[0].MigrationCount != 1 {
		t.Fatalf("unexpected database details: %+v", report.Databases)
	}
}

func TestRunValidateJSON_NoTxError(t *testing.T) {
	cfgPath := setupValidateTestWorkspace(
		t,
		"20261127090500_order_add_idx.sql",
		"-- migrate:service order\n-- migrate:transaction true\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_id ON public.orders (id);\n",
	)

	var buf bytes.Buffer
	err := runValidate([]string{"--config=" + cfgPath, "--db=all", "--json"}, &buf)
	if err == nil {
		t.Fatalf("expected runValidate error for CONCURRENTLY inside tx")
	}

	var report validateReportJSON
	if uErr := json.Unmarshal(buf.Bytes(), &report); uErr != nil {
		t.Fatalf("expected valid JSON even on lint error, got err=%v raw:\n%s", uErr, buf.String())
	}

	if report.Passed || report.ErrorCount < 1 {
		t.Fatalf("expected passed=false and errorCount>=1, got %+v", report)
	}
	found := false
	for _, iss := range report.Databases[0].Issues {
		if iss.Rule == "NO_TX_REQUIRED" && iss.Severity == migration.SeverityError {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected NO_TX_REQUIRED issue in %+v", report.Databases[0].Issues)
	}
}

func TestRunValidateJSON_StrictWarning(t *testing.T) {
	cfgPath := setupValidateTestWorkspace(
		t,
		"20261127091000_order_blocking_idx.sql",
		"-- migrate:service order\n-- migrate:transaction true\nCREATE INDEX idx_orders_blocking ON public.orders (id);\n",
	)

	var bufNonStrict bytes.Buffer
	if err := runValidate([]string{"--config=" + cfgPath, "--db=all", "--json"}, &bufNonStrict); err != nil {
		t.Fatalf("expected warning-only validation to pass without --strict, got %v", err)
	}

	var bufStrict bytes.Buffer
	err := runValidate([]string{"--config=" + cfgPath, "--db=all", "--strict", "--json"}, &bufStrict)
	if err == nil {
		t.Fatalf("expected validation error when --strict is enabled on warning")
	}

	var report validateReportJSON
	if uErr := json.Unmarshal(bufStrict.Bytes(), &report); uErr != nil {
		t.Fatalf("invalid strict json output: %v", uErr)
	}
	if report.Passed || !report.Strict || report.WarningCount < 1 {
		t.Fatalf("unexpected strict report: %+v", report)
	}
}

func TestToStatusDBJSON(t *testing.T) {
	appliedAt := time.Date(2026, 11, 27, 4, 15, 30, 0, time.UTC)
	res := runner.StatusResult{
		DBName:       "db_name",
		ActualDBName: "db_name_dev",
		AppliedCount: 1,
		PendingCount: 1,
		Mismatch:     1,
		DBOnlyCount:  1,
		Items: []runner.StatusItem{
			{
				Version:     "20261126090000",
				Filename:    "20261126090000_order_init.sql",
				Service:     "order",
				State:       runner.StateApplied,
				AppliedAt:   &appliedAt,
				ExecutionMS: 42,
				TriggeredBy: "dashboard:dev1",
				GitBranch:   "development",
			},
			{
				Version:  "20261127090000",
				Filename: "20261127090000_order_pending.sql",
				Service:  "order",
				State:    runner.StatePending,
			},
		},
		LintIssues: []migration.Issue{
			{
				Severity: migration.SeverityWarning,
				Rule:     "BLOCKING_INDEX",
				File:     "20261127090000_order_pending.sql",
				Message:  "sample warning",
			},
		},
	}

	dto := toStatusDBJSON(res)
	encoded, err := json.Marshal(statusReportJSON{
		Databases:   []statusDBJSON{dto},
		HasMismatch: true,
	})
	if err != nil {
		t.Fatalf("marshal statusReportJSON failed: %v", err)
	}

	var decoded statusReportJSON
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal statusReportJSON failed: %v", err)
	}

	if !decoded.HasMismatch || len(decoded.Databases) != 1 {
		t.Fatalf("unexpected decoded statusReportJSON: %+v", decoded)
	}
	db := decoded.Databases[0]
	if db.DBName != "db_name" || db.ActualDBName != "db_name_dev" || db.DBOnlyCount != 1 || db.MismatchCount != 1 {
		t.Fatalf("unexpected statusDBJSON fields: %+v", db)
	}
	if len(db.Items) != 2 || db.Items[0].AppliedAt == nil || *db.Items[0].AppliedAt != "2026-11-27T04:15:30Z" {
		t.Fatalf("unexpected status items: %+v", db.Items)
	}
	if db.Items[1].AppliedAt != nil {
		t.Fatalf("expected nil AppliedAt for pending item, got %v", *db.Items[1].AppliedAt)
	}
	if len(db.LintIssues) != 1 || db.LintIssues[0].Rule != "BLOCKING_INDEX" {
		t.Fatalf("unexpected lintIssues: %+v", db.LintIssues)
	}
}
