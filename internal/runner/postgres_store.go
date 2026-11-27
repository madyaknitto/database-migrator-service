package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresStore mengimplementasikan Store memakai satu koneksi pgx.Conn per database
// agar advisory lock, session timeout, dan transaksi selalu berada di session yang sama.
type PostgresStore struct {
	conn *pgx.Conn
}

// ConnectPostgres membuka koneksi tunggal PostgreSQL dan mengembalikan Store.
func ConnectPostgres(ctx context.Context, dsn string) (Store, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{conn: conn}, nil
}

func sanitizeTable(schema, table string) string {
	return pgx.Identifier{schema, table}.Sanitize()
}

func (s *PostgresStore) TryAdvisoryLock(ctx context.Context, lockKey int64) (bool, error) {
	var acquired bool
	err := s.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&acquired)
	return acquired, err
}

func (s *PostgresStore) UnlockAdvisory(ctx context.Context, lockKey int64) error {
	var unlocked bool
	err := s.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&unlocked)
	return err
}

func (s *PostgresStore) EnsureTrackingTable(ctx context.Context, schema, table string) error {
	ident := sanitizeTable(schema, table)
	query := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	version       VARCHAR(64)  PRIMARY KEY,
	filename      VARCHAR(255) NOT NULL,
	checksum      CHAR(64)     NOT NULL,
	service_name  VARCHAR(100) NOT NULL,
	triggered_by  VARCHAR(150) NOT NULL,
	git_branch    VARCHAR(100) NOT NULL,
	git_commit    VARCHAR(64)  NOT NULL,
	execution_ms  BIGINT       NOT NULL,
	applied_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
)`, ident)
	_, err := s.conn.Exec(ctx, query)
	return err
}

func (s *PostgresStore) ListApplied(ctx context.Context, schema, table string) ([]AppliedRecord, error) {
	ident := sanitizeTable(schema, table)
	query := fmt.Sprintf(`
SELECT version, filename, checksum, service_name, triggered_by, git_branch, git_commit, execution_ms, applied_at
FROM %s
ORDER BY version ASC`, ident)

	rows, err := s.conn.Query(ctx, query)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			// Tabel tracking belum ada: kembalikan daftar kosong agar perintah status tetap read-only.
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var out []AppliedRecord
	for rows.Next() {
		var rec AppliedRecord
		if scanErr := rows.Scan(
			&rec.Version,
			&rec.Filename,
			&rec.Checksum,
			&rec.ServiceName,
			&rec.TriggeredBy,
			&rec.GitBranch,
			&rec.GitCommit,
			&rec.ExecutionMS,
			&rec.AppliedAt,
		); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, rec)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return out, nil
}

func (s *PostgresStore) ExecInTx(
	ctx context.Context,
	lockTimeout, stmtTimeout, sql string,
	record AppliedRecord,
	schema, table string,
) error {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gagal memulai transaksi: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	if lockTimeout != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", lockTimeout); err != nil {
			return fmt.Errorf("gagal menyetel lock_timeout: %w", err)
		}
	}
	if stmtTimeout != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true)", stmtTimeout); err != nil {
			return fmt.Errorf("gagal menyetel statement_timeout: %w", err)
		}
	}

	start := time.Now()
	if _, err := tx.Exec(ctx, sql); err != nil {
		return formatPostgresError("eksekusi SQL gagal", err)
	}
	record.ExecutionMS = time.Since(start).Milliseconds()

	if err := insertRecordOnExecutor(ctx, tx, schema, table, record); err != nil {
		return fmt.Errorf("gagal mencatat riwayat migrasi: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("gagal commit transaksi migrasi: %w", err)
	}
	return nil
}

func (s *PostgresStore) ExecNoTx(
	ctx context.Context,
	lockTimeout, stmtTimeout string,
	statements []string,
	record AppliedRecord,
	schema, table string,
) error {
	if lockTimeout != "" {
		if _, err := s.conn.Exec(ctx, "SELECT set_config('lock_timeout', $1, false)", lockTimeout); err != nil {
			return fmt.Errorf("gagal menyetel session lock_timeout: %w", err)
		}
	}
	if stmtTimeout != "" {
		if _, err := s.conn.Exec(ctx, "SELECT set_config('statement_timeout', $1, false)", stmtTimeout); err != nil {
			return fmt.Errorf("gagal menyetel session statement_timeout: %w", err)
		}
	}
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.conn.Exec(resetCtx, "RESET lock_timeout; RESET statement_timeout;")
	}()

	start := time.Now()
	for idx, stmt := range statements {
		if _, err := s.conn.Exec(ctx, stmt); err != nil {
			return formatPostgresError(fmt.Sprintf("statement #%d gagal", idx+1), err)
		}
	}
	record.ExecutionMS = time.Since(start).Milliseconds()

	if err := s.InsertRecord(ctx, schema, table, record); err != nil {
		return fmt.Errorf("gagal mencatat riwayat migrasi setelah eksekusi no-tx: %w", err)
	}
	return nil
}

func (s *PostgresStore) InsertRecord(ctx context.Context, schema, table string, record AppliedRecord) error {
	return insertRecordOnExecutor(ctx, s.conn, schema, table, record)
}

func (s *PostgresStore) Close(ctx context.Context) error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close(ctx)
}

type sqlExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertRecordOnExecutor(ctx context.Context, exec sqlExecutor, schema, table string, rec AppliedRecord) error {
	ident := sanitizeTable(schema, table)
	query := fmt.Sprintf(`
INSERT INTO %s (
	version, filename, checksum, service_name, triggered_by, git_branch, git_commit, execution_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, ident)
	_, err := exec.Exec(
		ctx,
		query,
		rec.Version,
		rec.Filename,
		rec.Checksum,
		rec.ServiceName,
		rec.TriggeredBy,
		rec.GitBranch,
		rec.GitCommit,
		rec.ExecutionMS,
	)
	return err
}

func formatPostgresError(prefix string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Detail != "" {
			return fmt.Errorf("%s [%s]: %s (%s)", prefix, pgErr.Code, pgErr.Message, pgErr.Detail)
		}
		return fmt.Errorf("%s [%s]: %s", prefix, pgErr.Code, pgErr.Message)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
