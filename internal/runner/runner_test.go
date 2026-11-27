package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockStore struct {
	mu           sync.Mutex
	lockHeld     bool
	lockBusyLeft int
	tableEnsured bool
	records      map[string]AppliedRecord
	inTxCalls    []string
	noTxCalls    [][]string
	insertCalls  []string
	closed       bool
}

func newMockStore() *mockStore {
	return &mockStore{
		records: make(map[string]AppliedRecord),
	}
}

func (m *mockStore) TryAdvisoryLock(_ context.Context, _ int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lockBusyLeft > 0 {
		m.lockBusyLeft--
		return false, nil
	}
	if m.lockHeld {
		return false, nil
	}
	m.lockHeld = true
	return true, nil
}

func (m *mockStore) UnlockAdvisory(_ context.Context, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockHeld = false
	return nil
}

func (m *mockStore) EnsureTrackingTable(_ context.Context, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tableEnsured = true
	return nil
}

func (m *mockStore) ListApplied(_ context.Context, _, _ string) ([]AppliedRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppliedRecord, 0, len(m.records))
	for _, r := range m.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func (m *mockStore) ExecInTx(_ context.Context, _, _, sql string, record AppliedRecord, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inTxCalls = append(m.inTxCalls, record.Filename)
	_ = sql
	record.AppliedAt = time.Now()
	m.records[record.Version] = record
	return nil
}

func (m *mockStore) ExecNoTx(_ context.Context, _, _ string, statements []string, record AppliedRecord, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.noTxCalls = append(m.noTxCalls, statements)
	record.AppliedAt = time.Now()
	m.records[record.Version] = record
	return nil
}

func (m *mockStore) InsertRecord(_ context.Context, _, _ string, record AppliedRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertCalls = append(m.insertCalls, record.Filename)
	record.AppliedAt = time.Now()
	m.records[record.Version] = record
	return nil
}

func (m *mockStore) Close(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func setupDBDir(t *testing.T, baselineSQL string, migFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if baselineSQL != "" {
		bDir := filepath.Join(dir, "baseline")
		if err := os.MkdirAll(bDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bDir, "00000000000000_initial_baseline.sql"), []byte(baselineSQL), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mDir := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(mDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, sql := range migFiles {
		if err := os.WriteFile(filepath.Join(mDir, name), []byte(sql), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func staticConnector(s Store) Connector {
	return func(_ context.Context) (Store, error) {
		return s, nil
	}
}

func TestUp_ZeroPendingNoOp(t *testing.T) {
	dir := setupDBDir(t, "", map[string]string{
		"20261126090000_order_init.sql": "CREATE TABLE orders (id INT);",
	})
	ms := newMockStore()
	// Pre-populate checksum sama
	res1, err := Up(context.Background(), staticConnector(ms), UpOptions{
		DBName:           "db_name",
		ActualDBName:     "db_name",
		Dir:              dir,
		TrackingSchema:   "public",
		TrackingTable:    "schema_migrations",
		LockTimeout:      "5s",
		StatementTimeout: "60s",
		LockWaitTimeout:  2 * time.Second,
	})
	if err != nil || len(res1.AppliedFiles) != 1 {
		t.Fatalf("run pertama gagal: res=%+v err=%v", res1, err)
	}

	// Run kedua harus 0 pending (no-op)
	ms.inTxCalls = nil
	res2, err := Up(context.Background(), staticConnector(ms), UpOptions{
		DBName:           "db_name",
		ActualDBName:     "db_name",
		Dir:              dir,
		TrackingSchema:   "public",
		TrackingTable:    "schema_migrations",
		LockTimeout:      "5s",
		StatementTimeout: "60s",
		LockWaitTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("run kedua gagal: %v", err)
	}
	if len(res2.PendingFiles) != 0 || len(res2.AppliedFiles) != 0 || res2.AlreadyCount != 1 {
		t.Fatalf("run kedua seharusnya no-op: %+v", res2)
	}
	if len(ms.inTxCalls) != 0 {
		t.Fatalf("tidak boleh ada eksekusi SQL saat no-op: %+v", ms.inTxCalls)
	}
}

func TestUp_OutOfOrderAndDBOnlyWarning(t *testing.T) {
	// Branch production baru memiliki feat-1 (09:00) dan feat-2 (14:00),
	// sedangkan DB sudah terlanjur punya feat-2 (14:00) dan sebuah feat-staging (16:00).
	dir := setupDBDir(t, "", map[string]string{
		"20261126090000_order_feat1.sql": "ALTER TABLE orders ADD COLUMN a INT;",
		"20261126140000_promo_feat2.sql": "CREATE TABLE vouchers (id INT);",
	})

	ms := newMockStore()
	// Simulasikan feat-2 (14:00) sudah apply terlebih dahulu
	_, err := Up(context.Background(), staticConnector(ms), UpOptions{
		DBName:           "db_name",
		ActualDBName:     "db_name",
		Dir:              setupDBDir(t, "", map[string]string{"20261126140000_promo_feat2.sql": "CREATE TABLE vouchers (id INT);"}),
		TrackingSchema:   "public",
		TrackingTable:    "schema_migrations",
		LockTimeout:      "5s",
		StatementTimeout: "60s",
		LockWaitTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("setup feat-2 gagal: %v", err)
	}

	// Tambahkan satu record DB_ONLY (misal sisa fitur lain di staging)
	ms.records["20261126160000"] = AppliedRecord{
		Version:  "20261126160000",
		Filename: "20261126160000_other_staging_only.sql",
		Checksum: "deadbeef",
	}
	ms.inTxCalls = nil

	res, err := Up(context.Background(), staticConnector(ms), UpOptions{
		DBName:           "db_name",
		ActualDBName:     "db_name",
		Dir:              dir,
		TrackingSchema:   "public",
		TrackingTable:    "schema_migrations",
		LockTimeout:      "5s",
		StatementTimeout: "60s",
		LockWaitTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("out-of-order Up gagal: %v", err)
	}
	if len(res.AppliedFiles) != 1 || res.AppliedFiles[0].Version != "20261126090000" {
		t.Fatalf("seharusnya mengeksekusi feat-1 (20261126090000), dapat: %+v", res.AppliedFiles)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "APPLIED_IN_DB_NOT_IN_BRANCH") {
		t.Fatalf("seharusnya ada warning APPLIED_IN_DB_NOT_IN_BRANCH, dapat: %+v", res.Warnings)
	}
}

func TestUp_ChecksumMismatch(t *testing.T) {
	dir := setupDBDir(t, "", map[string]string{
		"20261126090000_order_feat1.sql": "ALTER TABLE orders ADD COLUMN a INT;",
	})
	ms := newMockStore()
	ms.records["20261126090000"] = AppliedRecord{
		Version:  "20261126090000",
		Filename: "20261126090000_order_feat1.sql",
		Checksum: "0000checksum_lama_berbeda",
	}

	_, err := Up(context.Background(), staticConnector(ms), UpOptions{
		DBName:           "db_name",
		ActualDBName:     "db_name",
		Dir:              dir,
		TrackingSchema:   "public",
		TrackingTable:    "schema_migrations",
		LockTimeout:      "5s",
		StatementTimeout: "60s",
		LockWaitTimeout:  time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("diharapkan error checksum mismatch, dapat: %v", err)
	}
}

func TestUp_LockContentionAndTimeout(t *testing.T) {
	dir := setupDBDir(t, "", map[string]string{
		"20261126090000_order_feat1.sql": "CREATE TABLE t (id INT);",
	})

	t.Run("lock retries then succeeds", func(t *testing.T) {
		ms := newMockStore()
		ms.lockBusyLeft = 2
		res, err := Up(context.Background(), staticConnector(ms), UpOptions{
			DBName:           "db_name",
			ActualDBName:     "db_name",
			Dir:              dir,
			TrackingSchema:   "public",
			TrackingTable:    "schema_migrations",
			LockTimeout:      "5s",
			StatementTimeout: "60s",
			LockWaitTimeout:  500 * time.Millisecond,
			PollInterval:     10 * time.Millisecond,
		})
		if err != nil || len(res.AppliedFiles) != 1 {
			t.Fatalf("diharapkan sukses setelah retry lock: res=%+v err=%v", res, err)
		}
		if ms.lockHeld {
			t.Fatal("advisory lock seharusnya dilepas setelah selesai")
		}
	})

	t.Run("lock times out", func(t *testing.T) {
		ms := newMockStore()
		ms.lockHeld = true
		_, err := Up(context.Background(), staticConnector(ms), UpOptions{
			DBName:           "db_name",
			ActualDBName:     "db_name",
			Dir:              dir,
			TrackingSchema:   "public",
			TrackingTable:    "schema_migrations",
			LockTimeout:      "5s",
			StatementTimeout: "60s",
			LockWaitTimeout:  40 * time.Millisecond,
			PollInterval:     10 * time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "timeout menunggu advisory lock") {
			t.Fatalf("diharapkan error timeout advisory lock, dapat: %v", err)
		}
	})
}

func TestUp_NoTxAndDryRun(t *testing.T) {
	dir := setupDBDir(t, "", map[string]string{
		"20261126090000_order_idx.sql": "-- migrate:transaction false\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON orders (a);\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx_b ON orders (b);",
	})

	t.Run("DryRun executes nothing", func(t *testing.T) {
		ms := newMockStore()
		res, err := Up(context.Background(), staticConnector(ms), UpOptions{
			DBName:           "db_name",
			ActualDBName:     "db_name",
			Dir:              dir,
			TrackingSchema:   "public",
			TrackingTable:    "schema_migrations",
			LockTimeout:      "5s",
			StatementTimeout: "60s",
			LockWaitTimeout:  time.Second,
			DryRun:           true,
		})
		if err != nil {
			t.Fatalf("DryRun gagal: %v", err)
		}
		if len(res.PendingFiles) != 1 || len(res.AppliedFiles) != 0 {
			t.Fatalf("DryRun hasil salah: %+v", res)
		}
		if len(ms.inTxCalls) != 0 || len(ms.noTxCalls) != 0 {
			t.Fatal("DryRun tidak boleh mengeksekusi statement")
		}
	})

	t.Run("transaction false uses ExecNoTx with split statements", func(t *testing.T) {
		ms := newMockStore()
		res, err := Up(context.Background(), staticConnector(ms), UpOptions{
			DBName:           "db_name",
			ActualDBName:     "db_name",
			Dir:              dir,
			TrackingSchema:   "public",
			TrackingTable:    "schema_migrations",
			LockTimeout:      "5s",
			StatementTimeout: "60s",
			LockWaitTimeout:  time.Second,
		})
		if err != nil {
			t.Fatalf("Up no-tx gagal: %v", err)
		}
		if len(res.AppliedFiles) != 1 {
			t.Fatalf("seharusnya 1 file ter-apply: %+v", res)
		}
		if len(ms.inTxCalls) != 0 || len(ms.noTxCalls) != 1 || len(ms.noTxCalls[0]) != 2 {
			t.Fatalf("ExecNoTx seharusnya dipanggil dengan 2 statement terpecah, got inTx=%v noTx=%v", ms.inTxCalls, ms.noTxCalls)
		}
	})
}

func TestUp_LintFailureNeverTouchesDB(t *testing.T) {
	dir := setupDBDir(t, "", map[string]string{
		"20261126090000_order_bad.sql": "DROP TABLE orders;",
	})
	called := false
	conn := func(_ context.Context) (Store, error) {
		called = true
		return newMockStore(), nil
	}
	_, err := Up(context.Background(), conn, UpOptions{
		DBName:         "db_name",
		Dir:            dir,
		TrackingSchema: "public",
		TrackingTable:  "schema_migrations",
	})
	if err == nil {
		t.Fatal("diharapkan error lint")
	}
	if called {
		t.Fatal("konektor DB tidak boleh dipanggil ketika validasi lint gagal")
	}
}

func TestBaseline_MarkerOnlyAndApplySQL(t *testing.T) {
	dir := setupDBDir(t, "CREATE TABLE existing_users (id INT PRIMARY KEY);", nil)

	t.Run("marker-only does not execute SQL and is idempotent", func(t *testing.T) {
		ms := newMockStore()
		opts := BaselineOptions{
			DBName:         "db_name",
			ActualDBName:   "db_name",
			Dir:            dir,
			TrackingSchema: "public",
			TrackingTable:  "schema_migrations",
			ApplySQL:       false,
		}

		res1, err := Baseline(context.Background(), staticConnector(ms), opts)
		if err != nil || res1.AlreadyBaselined {
			t.Fatalf("baseline pertama gagal: res=%+v err=%v", res1, err)
		}
		if len(ms.insertCalls) != 1 || len(ms.inTxCalls) != 0 {
			t.Fatalf("marker-only seharusnya hanya memanggil InsertRecord, bukan ExecInTx")
		}

		res2, err := Baseline(context.Background(), staticConnector(ms), opts)
		if err != nil || !res2.AlreadyBaselined {
			t.Fatalf("baseline kedua seharusnya idempotent (AlreadyBaselined=true): res=%+v err=%v", res2, err)
		}
	})

	t.Run("apply-sql runs on empty DB and rejects non-empty DB", func(t *testing.T) {
		ms := newMockStore()
		opts := BaselineOptions{
			DBName:           "db_name",
			ActualDBName:     "db_name",
			Dir:              dir,
			TrackingSchema:   "public",
			TrackingTable:    "schema_migrations",
			LockTimeout:      "5s",
			StatementTimeout: "60s",
			ApplySQL:         true,
		}
		res, err := Baseline(context.Background(), staticConnector(ms), opts)
		if err != nil || len(ms.inTxCalls) != 1 {
			t.Fatalf("apply-sql pada DB kosong gagal: res=%+v err=%v", res, err)
		}

		// Coba pada DB yang sudah punya migrasi lain (tanpa baseline marker) -> harus ditolak
		msOther := newMockStore()
		msOther.records["20261126090000"] = AppliedRecord{Version: "20261126090000"}
		_, err = Baseline(context.Background(), staticConnector(msOther), opts)
		if err == nil || !strings.Contains(err.Error(), "menolak --apply-sql") {
			t.Fatalf("seharusnya menolak --apply-sql pada DB yang sudah punya riwayat migrasi, dapat: %v", err)
		}
	})
}

func TestFormatPostgresDuration(t *testing.T) {
	if got, err := FormatPostgresDuration("1m30s"); err != nil || got != "90000ms" {
		t.Fatalf("FormatPostgresDuration(1m30s) = %q, %v; ingin 90000ms", got, err)
	}
	if got, err := FormatPostgresDuration("5s"); err != nil || got != "5000ms" {
		t.Fatalf("FormatPostgresDuration(5s) = %q, %v; ingin 5000ms", got, err)
	}
	if _, err := FormatPostgresDuration("-2s"); err == nil {
		t.Fatal("diharapkan error untuk durasi negatif")
	}
	_ = fmt.Sprintf("")
}
