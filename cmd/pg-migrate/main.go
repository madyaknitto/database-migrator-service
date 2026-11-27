package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"database-migrator-service/internal/config"
	"database-migrator-service/internal/migration"
	"database-migrator-service/internal/runner"
)

var (
	version = "0.1.0"
	commit  = "dev"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := args[0]
	rest := args[1:]

	var err error
	switch cmd {
	case "up":
		err = cmdUp(ctx, rest)
	case "status":
		err = cmdStatus(ctx, rest)
	case "validate":
		err = cmdValidate(rest)
	case "create":
		err = cmdCreate(rest)
	case "baseline":
		err = cmdBaseline(ctx, rest)
	case "version", "-v", "--version":
		fmt.Printf("pg-migrate v%s (commit: %s)\n", version, commit)
		return 0
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Perintah tidak dikenal: %q\n\n", cmd)
		printUsage()
		return 1
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Println(`pg-migrate - Centralized Multi-Database PostgreSQL Migration Runner

Penggunaan:
  pg-migrate <perintah> [opsi]

Perintah:
  up         Validasi SQL, ambil advisory lock, lalu jalankan migrasi pending
  status     Lihat status migrasi (APPLIED, PENDING, BASELINE, DB_ONLY) secara read-only
  validate   Validasi struktur file, directive, dan SQL safety linter tanpa menyentuh DB
  create     Buat file migrasi baru dengan timestamp UTC dan template header standar
  baseline   Tandai schema existing pada database sebagai baseline awal (atau --apply-sql untuk DB kosong)
  version    Tampilkan versi pg-migrate

Gunakan 'pg-migrate <perintah> --help' untuk melihat opsi masing-masing perintah.`)
}

func cmdUp(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	configPath := fs.String("config", "databases.yaml", "Path ke file konfigurasi databases.yaml")
	dbSelector := fs.String("db", "all", "Nama database target ('all' atau daftar dipisah koma, mis. db_name)")
	dryRun := fs.Bool("dry-run", false, "Hanya tampilkan antrian migrasi tanpa mengeksekusi SQL")
	strict := fs.Bool("strict", false, "Perlakukan WARNING linter sebagai ERROR")
	triggeredBy := fs.String("triggered-by", "", "Identitas pemicu (default: MIGRATE_TRIGGERED_BY / BUILD_TAG / manual)")
	gitBranch := fs.String("git-branch", "", "Nama branch (default: GIT_BRANCH / BRANCH_NAME / unknown)")
	gitCommit := fs.String("git-commit", "", "SHA commit (default: GIT_COMMIT / unknown)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, selected, resolved, err := loadAndResolve(configPath, dbSelector)
	if err != nil {
		return err
	}

	waitTimeout, err := cfg.LockWaitDuration()
	if err != nil {
		return err
	}

	trig := firstNonEmpty(*triggeredBy, os.Getenv("MIGRATE_TRIGGERED_BY"), os.Getenv("BUILD_TAG"), "manual")
	branch := firstNonEmpty(*gitBranch, os.Getenv("GIT_BRANCH"), os.Getenv("BRANCH_NAME"), "unknown")
	sha := firstNonEmpty(*gitCommit, os.Getenv("GIT_COMMIT"), "unknown")

	fmt.Printf("=== pg-migrate up (env-branch=%s, triggered-by=%s, dry-run=%v, target=%d DB) ===\n",
		branch, trig, *dryRun, len(selected))

	results := make([]runner.UpResult, 0, len(resolved))
	for _, rdb := range resolved {
		fmt.Printf("\n--- Database: %s (actual: %s, dir: %s) ---\n", rdb.Config.Name, rdb.ActualDBName, rdb.Config.Dir)

		dsn := rdb.DSN
		res, upErr := runner.Up(ctx, func(c context.Context) (runner.Store, error) {
			return runner.ConnectPostgres(c, dsn)
		}, runner.UpOptions{
			DBName:           rdb.Config.Name,
			ActualDBName:     rdb.ActualDBName,
			Dir:              cfg.ResolveDir(rdb.Config),
			TrackingSchema:   cfg.Defaults.TrackingSchema,
			TrackingTable:    cfg.Defaults.TrackingTable,
			LockTimeout:      cfg.Defaults.LockTimeout,
			StatementTimeout: cfg.Defaults.StatementTimeout,
			LockWaitTimeout:  waitTimeout,
			PollInterval:     runner.DefaultPollInterval,
			DryRun:           *dryRun,
			Strict:           *strict,
			TriggeredBy:      trig,
			GitBranch:        branch,
			GitCommit:        sha,
			Logf: func(format string, a ...any) {
				fmt.Printf(format+"\n", a...)
			},
		})
		if upErr != nil {
			printLintSummary(res.LintIssues)
			return upErr
		}

		for _, w := range res.Warnings {
			fmt.Printf("WARN: %s\n", w)
		}

		if len(res.PendingFiles) == 0 {
			fmt.Printf("[%s] Tidak ada antrian migrasi (sudah up to date, %d terpasang).\n", rdb.Config.Name, res.AlreadyCount)
		} else if *dryRun {
			fmt.Printf("[%s] DRY-RUN: %d migrasi menunggu dieksekusi:\n", rdb.Config.Name, len(res.PendingFiles))
			for _, f := range res.PendingFiles {
				fmt.Printf("  - %s (service=%s, tx=%v)\n", f.Filename, f.Service, f.Directives.Transaction)
			}
		} else {
			fmt.Printf("[%s] Selesai mengeksekusi %d migrasi baru.\n", rdb.Config.Name, len(res.AppliedFiles))
			for _, rec := range res.AppliedFiles {
				fmt.Printf("  + %s (%dms)\n", rec.Filename, rec.ExecutionMS)
			}
		}

		results = append(results, res)
	}

	fmt.Println("\n=== Ringkasan Eksekusi ===")
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DATABASE\tACTUAL DB\tSUDAH ADA\tPENDING\tDIEKSEKUSI\tSTATUS")
	for _, res := range results {
		status := "UP_TO_DATE"
		if *dryRun && len(res.PendingFiles) > 0 {
			status = "DRY_RUN_PENDING"
		} else if len(res.AppliedFiles) > 0 {
			status = "APPLIED"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n",
			res.DBName, res.ActualDBName, res.AlreadyCount, len(res.PendingFiles), len(res.AppliedFiles), status)
	}
	_ = tw.Flush()

	return nil
}

func cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	configPath := fs.String("config", "databases.yaml", "Path ke file konfigurasi databases.yaml")
	dbSelector := fs.String("db", "all", "Nama database target ('all' atau daftar dipisah koma, mis. db_name)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, resolved, err := loadAndResolve(configPath, dbSelector)
	if err != nil {
		return err
	}

	hasMismatch := false
	for _, rdb := range resolved {
		dsn := rdb.DSN
		res, stErr := runner.Status(ctx, func(c context.Context) (runner.Store, error) {
			return runner.ConnectPostgres(c, dsn)
		}, runner.StatusOptions{
			DBName:         rdb.Config.Name,
			ActualDBName:   rdb.ActualDBName,
			Dir:            cfg.ResolveDir(rdb.Config),
			TrackingSchema: cfg.Defaults.TrackingSchema,
			TrackingTable:  cfg.Defaults.TrackingTable,
		})
		if stErr != nil {
			return stErr
		}

		fmt.Printf("\n=== Status Database: %s (actual: %s) ===\n", res.DBName, res.ActualDBName)
		fmt.Printf("Ringkasan: applied=%d, pending=%d, mismatch=%d, db_only=%d\n",
			res.AppliedCount, res.PendingCount, res.Mismatch, res.DBOnlyCount)

		if len(res.Items) == 0 {
			fmt.Println("(belum ada file migrasi maupun riwayat di database)")
		} else {
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "STATE\tVERSION\tSERVICE\tFILENAME\tAPPLIED_AT\tTRIGGERED_BY\tNOTE")
			for _, item := range res.Items {
				appliedAt := "-"
				if item.AppliedAt != nil && !item.AppliedAt.IsZero() {
					appliedAt = item.AppliedAt.UTC().Format("2006-01-02 15:04:05Z")
				}
				trig := item.TriggeredBy
				if trig == "" {
					trig = "-"
				}
				note := item.Note
				if note == "" {
					note = "-"
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					item.State, item.Version, item.Service, item.Filename, appliedAt, trig, note)
			}
			_ = tw.Flush()
		}

		printLintSummary(res.LintIssues)
		if res.Mismatch > 0 {
			hasMismatch = true
		}
	}

	if hasMismatch {
		return fmt.Errorf("ditemukan CHECKSUM_MISMATCH pada salah satu database")
	}
	return nil
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	configPath := fs.String("config", "databases.yaml", "Path ke file konfigurasi databases.yaml")
	dbSelector := fs.String("db", "all", "Nama database target ('all' atau daftar dipisah koma, mis. db_name)")
	strict := fs.Bool("strict", false, "Perlakukan WARNING linter sebagai ERROR")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	selected, err := cfg.SelectDatabases(*dbSelector)
	if err != nil {
		return err
	}

	totalFiles := 0
	totalErrors := 0
	totalWarnings := 0

	for _, db := range selected {
		baseDir := cfg.ResolveDir(db)
		baseline, migs, err := migration.LoadDatabaseFiles(baseDir)
		if err != nil {
			return fmt.Errorf("[%s] %w", db.Name, err)
		}

		var files []migration.File
		if baseline != nil {
			files = append(files, *baseline)
		}
		files = append(files, migs...)
		totalFiles += len(files)

		issues := migration.LintAll(files)
		for _, iss := range issues {
			fmt.Printf("[%s] %s\n", db.Name, iss.String())
			if iss.Severity == migration.SeverityError {
				totalErrors++
			} else {
				totalWarnings++
			}
		}
		fmt.Printf("OK [%s]: %d file migrasi terverifikasi (baseline=%v)\n", db.Name, len(migs), baseline != nil)
	}

	fmt.Printf("\nValidasi selesai: %d database, %d file diperiksa, %d error, %d warning.\n",
		len(selected), totalFiles, totalErrors, totalWarnings)

	if totalErrors > 0 || (*strict && totalWarnings > 0) {
		return fmt.Errorf("validasi gagal (errors=%d, warnings=%d, strict=%v)", totalErrors, totalWarnings, *strict)
	}
	return nil
}

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	configPath := fs.String("config", "databases.yaml", "Path ke file konfigurasi databases.yaml")
	dbName := fs.String("db", "", "Nama database target di databases.yaml (wajib, mis. db_name)")
	service := fs.String("service", "", "Nama service/domain pembuat migrasi, huruf kecil/angka/'-' (wajib)")
	name := fs.String("name", "", "Deskripsi singkat snake_case (wajib)")
	ticket := fs.String("ticket", "", "Nomor tiket/issue opsional, mis. PROJ-123")
	noTx := fs.Bool("no-tx", false, "Nonaktifkan blok transaksi (untuk CREATE INDEX CONCURRENTLY)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*dbName) == "" {
		return fmt.Errorf("flag --db wajib diisi")
	}
	svc := strings.ToLower(strings.TrimSpace(*service))
	if !migration.ServiceNameRegex.MatchString(svc) {
		return fmt.Errorf("--service %q tidak valid (harus huruf kecil diawali huruf, hanya a-z, 0-9, '-')", *service)
	}

	desc := normalizeDesc(*name)
	if !migration.DescriptionRegex.MatchString(desc) {
		return fmt.Errorf("--name %q tidak valid (gunakan huruf kecil/angka/underscore, mis. add_orders_discount)", *name)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	selected, err := cfg.SelectDatabases(*dbName)
	if err != nil {
		return err
	}
	if len(selected) != 1 {
		return fmt.Errorf("perintah create hanya menerima tepat 1 database")
	}

	migDir := filepath.Join(cfg.ResolveDir(selected[0]), "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		return fmt.Errorf("gagal membuat folder %q: %w", migDir, err)
	}

	// Pastikan timestamp UTC unik meski dipanggil beruntun di detik yang sama
	tsTime := time.Now().UTC()
	var fullPath, filename string
	for {
		ts := tsTime.Format("20060102150405")
		filename = fmt.Sprintf("%s_%s_%s.sql", ts, svc, desc)
		fullPath = filepath.Join(migDir, filename)
		if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			break
		}
		tsTime = tsTime.Add(1 * time.Second)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("-- migrate:service %s\n", svc))
	if strings.TrimSpace(*ticket) != "" {
		sb.WriteString(fmt.Sprintf("-- migrate:ticket %s\n", strings.TrimSpace(*ticket)))
	}
	if *noTx {
		sb.WriteString("-- migrate:transaction false\n")
		sb.WriteString("-- migrate:statement-timeout 10m\n")
	} else {
		sb.WriteString("-- migrate:transaction true\n")
	}
	sb.WriteString(fmt.Sprintf("-- Database: %s\n", selected[0].Name))
	sb.WriteString("-- Catatan: file yang sudah masuk development/staging/production TIDAK BOLEH diedit; buat file baru jika ada revisi.\n\n")
	if *noTx {
		sb.WriteString("-- Contoh non-transactional DDL PostgreSQL:\n-- CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_contoh ON public.nama_tabel (kolom);\n")
	} else {
		sb.WriteString("-- Tulis DDL PostgreSQL yang backward-compatible di bawah ini:\n")
	}

	if err := os.WriteFile(fullPath, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("gagal menulis file migrasi %q: %w", fullPath, err)
	}

	fmt.Printf("File migrasi berhasil dibuat:\n  %s\n", fullPath)
	return nil
}

func cmdBaseline(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	configPath := fs.String("config", "databases.yaml", "Path ke file konfigurasi databases.yaml")
	dbSelector := fs.String("db", "all", "Nama database target ('all' atau daftar dipisah koma, mis. db_name)")
	applySQL := fs.Bool("apply-sql", false, "Eksekusi SQL baseline (HANYA untuk DB baru yang masih kosong)")
	triggeredBy := fs.String("triggered-by", "", "Identitas pemicu (default: MIGRATE_TRIGGERED_BY / BUILD_TAG / manual)")
	gitBranch := fs.String("git-branch", "", "Nama branch (default: GIT_BRANCH / BRANCH_NAME / unknown)")
	gitCommit := fs.String("git-commit", "", "SHA commit (default: GIT_COMMIT / unknown)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, resolved, err := loadAndResolve(configPath, dbSelector)
	if err != nil {
		return err
	}

	waitTimeout, err := cfg.LockWaitDuration()
	if err != nil {
		return err
	}

	trig := firstNonEmpty(*triggeredBy, os.Getenv("MIGRATE_TRIGGERED_BY"), os.Getenv("BUILD_TAG"), "manual")
	branch := firstNonEmpty(*gitBranch, os.Getenv("GIT_BRANCH"), os.Getenv("BRANCH_NAME"), "unknown")
	sha := firstNonEmpty(*gitCommit, os.Getenv("GIT_COMMIT"), "unknown")

	for _, rdb := range resolved {
		dsn := rdb.DSN
		res, baseErr := runner.Baseline(ctx, func(c context.Context) (runner.Store, error) {
			return runner.ConnectPostgres(c, dsn)
		}, runner.BaselineOptions{
			DBName:           rdb.Config.Name,
			ActualDBName:     rdb.ActualDBName,
			Dir:              cfg.ResolveDir(rdb.Config),
			TrackingSchema:   cfg.Defaults.TrackingSchema,
			TrackingTable:    cfg.Defaults.TrackingTable,
			LockTimeout:      cfg.Defaults.LockTimeout,
			StatementTimeout: cfg.Defaults.StatementTimeout,
			LockWaitTimeout:  waitTimeout,
			PollInterval:     runner.DefaultPollInterval,
			ApplySQL:         *applySQL,
			TriggeredBy:      trig,
			GitBranch:        branch,
			GitCommit:        sha,
			Logf: func(format string, a ...any) {
				fmt.Printf(format+"\n", a...)
			},
		})
		if baseErr != nil {
			return baseErr
		}

		if res.AlreadyBaselined {
			fmt.Printf("[%s] Baseline %s sudah tercatat sebelumnya (idempotent skip).\n", res.DBName, res.Filename)
		} else if res.AppliedSQL {
			fmt.Printf("[%s] SQL Baseline %s berhasil dieksekusi & dicatat (%dms).\n", res.DBName, res.Filename, res.ExecutionMS)
		} else {
			fmt.Printf("[%s] Marker baseline %s berhasil dicatat tanpa mengeksekusi SQL (mode DB existing).\n", res.DBName, res.Filename)
		}
	}

	return nil
}

func loadAndResolve(configPath, dbSelector *string) (*config.Config, []config.DatabaseConfig, []config.ResolvedDatabase, error) {
	cfg, err := config.Load(*configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	selected, err := cfg.SelectDatabases(*dbSelector)
	if err != nil {
		return nil, nil, nil, err
	}
	resolved, err := config.ResolveAll(selected, os.LookupEnv)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, selected, resolved, nil
}

func printLintSummary(issues []migration.Issue) {
	for _, iss := range issues {
		fmt.Printf("LINT %s\n", iss.String())
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeDesc(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}
