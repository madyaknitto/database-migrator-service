package runner

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"database-migrator-service/internal/migration"
)

const (
	DefaultPollInterval = 500 * time.Millisecond
	lockLogInterval     = 5 * time.Second
)

// AppliedRecord merepresentasikan satu baris pada tabel tracking schema_migrations.
type AppliedRecord struct {
	Version     string
	Filename    string
	Checksum    string
	ServiceName string
	TriggeredBy string
	GitBranch   string
	GitCommit   string
	ExecutionMS int64
	AppliedAt   time.Time
}

// Store mendefinisikan operasi database PostgreSQL yang dibutuhkan runner.
type Store interface {
	TryAdvisoryLock(ctx context.Context, lockKey int64) (bool, error)
	UnlockAdvisory(ctx context.Context, lockKey int64) error
	EnsureTrackingTable(ctx context.Context, schema, table string) error
	ListApplied(ctx context.Context, schema, table string) ([]AppliedRecord, error)
	ExecInTx(ctx context.Context, lockTimeout, stmtTimeout, sql string, record AppliedRecord, schema, table string) error
	ExecNoTx(ctx context.Context, lockTimeout, stmtTimeout string, statements []string, record AppliedRecord, schema, table string) error
	InsertRecord(ctx context.Context, schema, table string, record AppliedRecord) error
	Close(ctx context.Context) error
}

// Connector membuka koneksi Store ke satu database target.
type Connector func(ctx context.Context) (Store, error)

// UpOptions mengatur perilaku eksekusi perintah up.
type UpOptions struct {
	DBName           string
	ActualDBName     string
	Dir              string
	TrackingSchema   string
	TrackingTable    string
	LockTimeout      string
	StatementTimeout string
	LockWaitTimeout  time.Duration
	PollInterval     time.Duration
	DryRun           bool
	Strict           bool
	TriggeredBy      string
	GitBranch        string
	GitCommit        string
	Logf             func(string, ...any)
}

// UpResult merangkum hasil eksekusi up pada satu database.
type UpResult struct {
	DBName       string
	ActualDBName string
	TotalLocal   int
	AlreadyCount int
	PendingFiles []migration.File
	AppliedFiles []AppliedRecord
	Warnings     []string
	LintIssues   []migration.Issue
	DryRun       bool
}

// StatusOptions mengatur perintah status.
type StatusOptions struct {
	DBName         string
	ActualDBName   string
	Dir            string
	TrackingSchema string
	TrackingTable  string
}

// StatusState menyatakan kondisi satu versi migrasi saat cek status.
type StatusState string

const (
	StateBaseline         StatusState = "BASELINE"
	StatePendingBaseline  StatusState = "PENDING_BASELINE"
	StateApplied          StatusState = "APPLIED"
	StatePending          StatusState = "PENDING"
	StateChecksumMismatch StatusState = "CHECKSUM_MISMATCH"
	StateDBOnly           StatusState = "DB_ONLY"
)

// StatusItem menggambarkan satu entri status migrasi.
type StatusItem struct {
	Version     string
	Filename    string
	Service     string
	State       StatusState
	AppliedAt   *time.Time
	ExecutionMS int64
	TriggeredBy string
	GitBranch   string
	Note        string
}

// StatusResult merangkum seluruh status migrasi satu database.
type StatusResult struct {
	DBName       string
	ActualDBName string
	Items        []StatusItem
	LintIssues   []migration.Issue
	PendingCount int
	AppliedCount int
	Mismatch     int
	DBOnlyCount  int
}

// BaselineOptions mengatur perintah baseline.
type BaselineOptions struct {
	DBName           string
	ActualDBName     string
	Dir              string
	TrackingSchema   string
	TrackingTable    string
	LockTimeout      string
	StatementTimeout string
	LockWaitTimeout  time.Duration
	PollInterval     time.Duration
	ApplySQL         bool
	TriggeredBy      string
	GitBranch        string
	GitCommit        string
	Logf             func(string, ...any)
}

// BaselineResult merangkum hasil perintah baseline.
type BaselineResult struct {
	DBName           string
	ActualDBName     string
	Filename         string
	Version          string
	AlreadyBaselined bool
	AppliedSQL       bool
	ExecutionMS      int64
}

// LockKey menghasilkan kunci advisory lock 64-bit deterministik per nama database.
func LockKey(dbName string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("database_migrator_service:" + dbName))
	return int64(h.Sum64())
}

// FormatPostgresDuration mengonversi durasi Go (mis. "1m30s", "5s") ke format milidetik PostgreSQL ("90000ms", "5000ms").
func FormatPostgresDuration(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return "", fmt.Errorf("durasi %q tidak valid: %w", raw, err)
	}
	if d <= 0 {
		return "", fmt.Errorf("durasi %q harus > 0", raw)
	}
	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return fmt.Sprintf("%dms", ms), nil
}

// AcquireLock mencoba mengambil PostgreSQL advisory lock dengan polling sampai waitTimeout tercapai.
func AcquireLock(
	ctx context.Context,
	store Store,
	lockKey int64,
	waitTimeout time.Duration,
	pollInterval time.Duration,
	logf func(string, ...any),
) error {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	if waitTimeout <= 0 {
		waitTimeout = 120 * time.Second
	}

	deadline := time.Now().Add(waitTimeout)
	lastLog := time.Time{}

	for {
		acquired, err := store.TryAdvisoryLock(ctx, lockKey)
		if err != nil {
			return fmt.Errorf("gagal mengambil advisory lock: %w", err)
		}
		if acquired {
			return nil
		}

		now := time.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("timeout menunggu advisory lock (key=%d) setelah %s; kemungkinan ada deployment lain sedang berjalan", lockKey, waitTimeout)
		}

		if logf != nil && (lastLog.IsZero() || now.Sub(lastLog) >= lockLogInterval) {
			remaining := deadline.Sub(now).Round(time.Second)
			logf("Menunggu advisory lock DB dilepas oleh proses lain (sisa waktu tunggu: %s)...", remaining)
			lastLog = now
		}

		sleepDur := pollInterval
		if rem := time.Until(deadline); rem < sleepDur {
			sleepDur = rem
		}

		timer := time.NewTimer(sleepDur)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("dibatalkan saat menunggu advisory lock: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// Up memvalidasi seluruh file lokal, mengambil advisory lock, mengecek checksum,
// dan mengeksekusi migrasi pending secara berurutan (mendukung out-of-order antar branch).
func Up(ctx context.Context, connect Connector, opts UpOptions) (UpResult, error) {
	res := UpResult{
		DBName:       opts.DBName,
		ActualDBName: opts.ActualDBName,
		DryRun:       opts.DryRun,
	}

	// 1. Muat dan validasi seluruh file SQL sebelum menyentuh database
	baselineFile, migFiles, err := migration.LoadDatabaseFiles(opts.Dir)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal memuat file migrasi: %w", opts.DBName, err)
	}
	res.TotalLocal = len(migFiles)

	var allToLint []migration.File
	if baselineFile != nil {
		allToLint = append(allToLint, *baselineFile)
	}
	allToLint = append(allToLint, migFiles...)

	issues := migration.LintAll(allToLint)
	res.LintIssues = issues
	if migration.HasErrors(issues, opts.Strict) {
		return res, fmt.Errorf("[%s] validasi SQL lint gagal (%d temuan); perbaiki sebelum menjalankan migrasi", opts.DBName, len(issues))
	}
	for _, iss := range issues {
		if iss.Severity == migration.SeverityWarning {
			res.Warnings = append(res.Warnings, iss.String())
		}
	}

	// 2. Koneksi khusus ke database target
	store, err := connect(ctx)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal terhubung ke database: %w", opts.DBName, err)
	}
	defer func() {
		_ = store.Close(context.Background())
	}()

	// 3. Acquire advisory lock
	lockKey := LockKey(lockKeySeed(opts.DBName, opts.ActualDBName))
	if err := AcquireLock(ctx, store, lockKey, opts.LockWaitTimeout, opts.PollInterval, opts.Logf); err != nil {
		return res, fmt.Errorf("[%s] %w", opts.DBName, err)
	}
	defer func() {
		_ = store.UnlockAdvisory(context.Background(), lockKey)
	}()

	// 4. Pastikan tabel tracking tersedia
	if err := store.EnsureTrackingTable(ctx, opts.TrackingSchema, opts.TrackingTable); err != nil {
		return res, fmt.Errorf("[%s] gagal menyiapkan tabel tracking %s.%s: %w", opts.DBName, opts.TrackingSchema, opts.TrackingTable, err)
	}

	// 5. Ambil riwayat migrasi yang sudah diterapkan
	appliedList, err := store.ListApplied(ctx, opts.TrackingSchema, opts.TrackingTable)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal membaca riwayat migrasi: %w", opts.DBName, err)
	}

	appliedMap := make(map[string]AppliedRecord, len(appliedList))
	for _, rec := range appliedList {
		appliedMap[rec.Version] = rec
	}

	// 6. Validasi checksum baseline (jika ada di DB & di disk) dan migrasi lokal
	localMap := make(map[string]migration.File, len(migFiles)+1)
	if baselineFile != nil {
		localMap[baselineFile.Version] = *baselineFile
		if rec, ok := appliedMap[baselineFile.Version]; ok {
			if rec.Checksum != baselineFile.Checksum {
				return res, fmt.Errorf(
					"[%s] checksum mismatch pada baseline %s: di DB=%s, di file=%s",
					opts.DBName, baselineFile.Filename, rec.Checksum, baselineFile.Checksum,
				)
			}
		}
	}

	var pending []migration.File
	for _, f := range migFiles {
		localMap[f.Version] = f
		if rec, ok := appliedMap[f.Version]; ok {
			if rec.Checksum != f.Checksum {
				return res, fmt.Errorf(
					"[%s] checksum mismatch pada %s (versi %s): di DB=%s, di file=%s (file yang sudah dieksekusi dilarang diubah!)",
					opts.DBName, f.Filename, f.Version, rec.Checksum, f.Checksum,
				)
			}
			res.AlreadyCount++
		} else {
			pending = append(pending, f)
		}
	}

	// 7. Deteksi migrasi yang tercatat di DB namun tidak ada di branch saat ini (misal fitur lain di development/staging)
	for _, rec := range appliedList {
		if _, existsLocally := localMap[rec.Version]; !existsLocally {
			if baselineFile == nil && strings.HasPrefix(rec.Version, "00000000000000") {
				res.Warnings = append(res.Warnings, fmt.Sprintf("APPLIED_IN_DB_NOT_IN_BRANCH: baseline %s (%s) ada di DB tetapi file baseline tidak ditemukan di branch ini", rec.Version, rec.Filename))
				continue
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf("APPLIED_IN_DB_NOT_IN_BRANCH: versi %s (%s) sudah tercatat di DB tetapi belum ada di branch ini (aman untuk skenario multi-branch)", rec.Version, rec.Filename))
		}
	}

	res.PendingFiles = pending

	if len(pending) == 0 || opts.DryRun {
		return res, nil
	}

	// 8. Eksekusi setiap migrasi pending secara berurutan
	triggeredBy := fallbackString(opts.TriggeredBy, "manual")
	gitBranch := fallbackString(opts.GitBranch, "unknown")
	gitCommit := fallbackString(opts.GitCommit, "unknown")

	for _, f := range pending {
		rawLock := opts.LockTimeout
		if f.Directives.LockTimeout != "" {
			rawLock = f.Directives.LockTimeout
		}
		rawStmt := opts.StatementTimeout
		if f.Directives.StatementTimeout != "" {
			rawStmt = f.Directives.StatementTimeout
		}

		pgLockTimeout, err := FormatPostgresDuration(rawLock)
		if err != nil {
			return res, fmt.Errorf("[%s] lock_timeout tidak valid pada %s: %w", opts.DBName, f.Filename, err)
		}
		pgStmtTimeout, err := FormatPostgresDuration(rawStmt)
		if err != nil {
			return res, fmt.Errorf("[%s] statement_timeout tidak valid pada %s: %w", opts.DBName, f.Filename, err)
		}

		record := AppliedRecord{
			Version:     f.Version,
			Filename:    f.Filename,
			Checksum:    f.Checksum,
			ServiceName: f.Service,
			TriggeredBy: triggeredBy,
			GitBranch:   gitBranch,
			GitCommit:   gitCommit,
		}

		if opts.Logf != nil {
			txMode := "tx"
			if !f.Directives.Transaction {
				txMode = "no-tx"
			}
			opts.Logf("[%s] Menjalankan %s (%s)...", opts.DBName, f.Filename, txMode)
		}

		start := time.Now()
		if f.Directives.Transaction {
			err = store.ExecInTx(ctx, pgLockTimeout, pgStmtTimeout, f.RawSQL, record, opts.TrackingSchema, opts.TrackingTable)
		} else {
			stmts := migration.SplitStatements(f.RawSQL)
			err = store.ExecNoTx(ctx, pgLockTimeout, pgStmtTimeout, stmts, record, opts.TrackingSchema, opts.TrackingTable)
		}
		elapsedMS := time.Since(start).Milliseconds()

		if err != nil {
			return res, fmt.Errorf("[%s] eksekusi %s gagal setelah %dms: %w", opts.DBName, f.Filename, elapsedMS, err)
		}

		record.ExecutionMS = elapsedMS
		record.AppliedAt = time.Now()
		res.AppliedFiles = append(res.AppliedFiles, record)
	}

	return res, nil
}

// Status membaca kondisi migrasi lokal terhadap tabel tracking di database (bersifat read-only).
func Status(ctx context.Context, connect Connector, opts StatusOptions) (StatusResult, error) {
	res := StatusResult{
		DBName:       opts.DBName,
		ActualDBName: opts.ActualDBName,
	}

	baselineFile, migFiles, err := migration.LoadDatabaseFiles(opts.Dir)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal memuat file migrasi: %w", opts.DBName, err)
	}

	var allToLint []migration.File
	if baselineFile != nil {
		allToLint = append(allToLint, *baselineFile)
	}
	allToLint = append(allToLint, migFiles...)
	res.LintIssues = migration.LintAll(allToLint)

	store, err := connect(ctx)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal terhubung ke database: %w", opts.DBName, err)
	}
	defer func() {
		_ = store.Close(context.Background())
	}()

	appliedList, err := store.ListApplied(ctx, opts.TrackingSchema, opts.TrackingTable)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal membaca riwayat migrasi: %w", opts.DBName, err)
	}

	appliedMap := make(map[string]AppliedRecord, len(appliedList))
	for _, rec := range appliedList {
		appliedMap[rec.Version] = rec
	}

	seenVersions := make(map[string]struct{}, len(migFiles)+1)

	if baselineFile != nil {
		seenVersions[baselineFile.Version] = struct{}{}
		item := StatusItem{
			Version:  baselineFile.Version,
			Filename: baselineFile.Filename,
			Service:  baselineFile.Service,
		}
		if rec, ok := appliedMap[baselineFile.Version]; ok {
			appliedAt := rec.AppliedAt
			item.AppliedAt = &appliedAt
			item.ExecutionMS = rec.ExecutionMS
			item.TriggeredBy = rec.TriggeredBy
			item.GitBranch = rec.GitBranch
			if rec.Checksum != baselineFile.Checksum {
				item.State = StateChecksumMismatch
				item.Note = "checksum baseline berbeda dengan DB"
				res.Mismatch++
			} else {
				item.State = StateBaseline
				res.AppliedCount++
			}
		} else {
			item.State = StatePendingBaseline
			item.Note = "jalankan 'pg-migrate baseline' pada DB existing"
		}
		res.Items = append(res.Items, item)
	}

	for _, f := range migFiles {
		seenVersions[f.Version] = struct{}{}
		item := StatusItem{
			Version:  f.Version,
			Filename: f.Filename,
			Service:  f.Service,
		}
		if rec, ok := appliedMap[f.Version]; ok {
			appliedAt := rec.AppliedAt
			item.AppliedAt = &appliedAt
			item.ExecutionMS = rec.ExecutionMS
			item.TriggeredBy = rec.TriggeredBy
			item.GitBranch = rec.GitBranch
			if rec.Checksum != f.Checksum {
				item.State = StateChecksumMismatch
				item.Note = "isi file berubah setelah dieksekusi di DB"
				res.Mismatch++
			} else {
				item.State = StateApplied
				res.AppliedCount++
			}
		} else {
			item.State = StatePending
			res.PendingCount++
		}
		res.Items = append(res.Items, item)
	}

	for _, rec := range appliedList {
		if _, ok := seenVersions[rec.Version]; ok {
			continue
		}
		appliedAt := rec.AppliedAt
		res.Items = append(res.Items, StatusItem{
			Version:     rec.Version,
			Filename:    rec.Filename,
			Service:     rec.ServiceName,
			State:       StateDBOnly,
			AppliedAt:   &appliedAt,
			ExecutionMS: rec.ExecutionMS,
			TriggeredBy: rec.TriggeredBy,
			GitBranch:   rec.GitBranch,
			Note:        "sudah ada di DB, tidak ditemukan di branch ini",
		})
		res.DBOnlyCount++
	}

	return res, nil
}

// Baseline menandai schema existing pada database (atau mengeksekusi SQL baseline pada fresh DB jika ApplySQL=true).
func Baseline(ctx context.Context, connect Connector, opts BaselineOptions) (BaselineResult, error) {
	res := BaselineResult{
		DBName:       opts.DBName,
		ActualDBName: opts.ActualDBName,
		AppliedSQL:   opts.ApplySQL,
	}

	baselineFile, _, err := migration.LoadDatabaseFiles(opts.Dir)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal memuat file baseline: %w", opts.DBName, err)
	}
	if baselineFile == nil {
		return res, fmt.Errorf("[%s] file baseline (00000000000000_*.sql) tidak ditemukan di %s/baseline", opts.DBName, opts.Dir)
	}

	res.Filename = baselineFile.Filename
	res.Version = baselineFile.Version

	issues := migration.LintFile(*baselineFile)
	if migration.HasErrors(issues, false) {
		return res, fmt.Errorf("[%s] file baseline tidak valid: %s", opts.DBName, issues[0].String())
	}

	store, err := connect(ctx)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal terhubung ke database: %w", opts.DBName, err)
	}
	defer func() {
		_ = store.Close(context.Background())
	}()

	lockKey := LockKey(lockKeySeed(opts.DBName, opts.ActualDBName))
	if err := AcquireLock(ctx, store, lockKey, opts.LockWaitTimeout, opts.PollInterval, opts.Logf); err != nil {
		return res, fmt.Errorf("[%s] %w", opts.DBName, err)
	}
	defer func() {
		_ = store.UnlockAdvisory(context.Background(), lockKey)
	}()

	if err := store.EnsureTrackingTable(ctx, opts.TrackingSchema, opts.TrackingTable); err != nil {
		return res, fmt.Errorf("[%s] gagal menyiapkan tabel tracking %s.%s: %w", opts.DBName, opts.TrackingSchema, opts.TrackingTable, err)
	}

	appliedList, err := store.ListApplied(ctx, opts.TrackingSchema, opts.TrackingTable)
	if err != nil {
		return res, fmt.Errorf("[%s] gagal membaca riwayat migrasi: %w", opts.DBName, err)
	}

	for _, rec := range appliedList {
		if rec.Version == baselineFile.Version {
			if rec.Checksum != baselineFile.Checksum {
				return res, fmt.Errorf(
					"[%s] checksum mismatch pada baseline %s: di DB=%s, di file=%s",
					opts.DBName, baselineFile.Filename, rec.Checksum, baselineFile.Checksum,
				)
			}
			res.AlreadyBaselined = true
			return res, nil
		}
	}

	record := AppliedRecord{
		Version:     baselineFile.Version,
		Filename:    baselineFile.Filename,
		Checksum:    baselineFile.Checksum,
		ServiceName: "baseline",
		TriggeredBy: fallbackString(opts.TriggeredBy, "manual"),
		GitBranch:   fallbackString(opts.GitBranch, "unknown"),
		GitCommit:   fallbackString(opts.GitCommit, "unknown"),
	}

	if !opts.ApplySQL {
		if err := store.InsertRecord(ctx, opts.TrackingSchema, opts.TrackingTable, record); err != nil {
			return res, fmt.Errorf("[%s] gagal mencatat marker baseline: %w", opts.DBName, err)
		}
		return res, nil
	}

	// Mode --apply-sql hanya aman untuk database yang belum punya riwayat migrasi sama sekali
	if len(appliedList) > 0 {
		return res, fmt.Errorf("[%s] menolak --apply-sql karena tabel tracking sudah memiliki %d entri migrasi", opts.DBName, len(appliedList))
	}

	pgLockTimeout, err := FormatPostgresDuration(opts.LockTimeout)
	if err != nil {
		return res, err
	}
	pgStmtTimeout, err := FormatPostgresDuration(opts.StatementTimeout)
	if err != nil {
		return res, err
	}

	start := time.Now()
	if baselineFile.Directives.Transaction {
		err = store.ExecInTx(ctx, pgLockTimeout, pgStmtTimeout, baselineFile.RawSQL, record, opts.TrackingSchema, opts.TrackingTable)
	} else {
		stmts := migration.SplitStatements(baselineFile.RawSQL)
		err = store.ExecNoTx(ctx, pgLockTimeout, pgStmtTimeout, stmts, record, opts.TrackingSchema, opts.TrackingTable)
	}
	res.ExecutionMS = time.Since(start).Milliseconds()

	if err != nil {
		return res, fmt.Errorf("[%s] gagal menerapkan SQL baseline %s: %w", opts.DBName, baselineFile.Filename, err)
	}

	return res, nil
}

func lockKeySeed(logicalName, actualName string) string {
	if strings.TrimSpace(actualName) != "" {
		return actualName
	}
	return logicalName
}

func fallbackString(val, fallback string) string {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	return strings.TrimSpace(val)
}
