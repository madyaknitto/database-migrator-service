package migration

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	SeverityError   = "ERROR"
	SeverityWarning = "WARNING"
)

// Issue merepresentasikan temuan SQL safety linter pada satu file migrasi.
type Issue struct {
	Severity string
	Rule     string
	File     string
	Message  string
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] %s (%s): %s", i.Severity, i.Rule, i.File, i.Message)
}

var (
	rePostgresNoTxKeywords          = regexp.MustCompile(`(?is)\b(?:CREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY|DROP\s+INDEX\s+CONCURRENTLY|REINDEX\s+(?:INDEX|TABLE|SCHEMA|DATABASE|SYSTEM)\s+CONCURRENTLY|VACUUM)\b`)
	reDropTable                     = regexp.MustCompile(`(?is)\bDROP\s+TABLE\b`)
	reTruncate                      = regexp.MustCompile(`(?is)\bTRUNCATE\b`)
	reAlterTable                    = regexp.MustCompile(`(?is)\bALTER\s+TABLE\b`)
	reAlterRename                   = regexp.MustCompile(`(?is)\bALTER\s+TABLE\b[\s\S]*\bRENAME\b`)
	reCreateTable                   = regexp.MustCompile(`(?is)\bCREATE\s+(?:(?:GLOBAL|LOCAL)\s+)?(?:TEMP(?:ORARY)?\s+|UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?((?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*)(?:\s*\.\s*(?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*))?)`)
	reCreateIndex                   = regexp.MustCompile(`(?is)\bCREATE\s+(UNIQUE\s+)?INDEX\s+(CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?(?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*)\s+ON\s+(?:ONLY\s+)?((?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*)(?:\s*\.\s*(?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*))?)`)
	rePostgresConcurrentIndex       = regexp.MustCompile(`(?is)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\b`)
	rePostgresConcurrentIfNotExists = regexp.MustCompile(`(?is)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\b`)
	reDropClause                    = regexp.MustCompile(`(?is)\bDROP\s+(?:IF\s+EXISTS\s+)?(?:(COLUMN|CONSTRAINT|DEFAULT|NOT\s+NULL|IDENTITY|EXPRESSION)\b|("[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*))`)
	reAddColumn                     = regexp.MustCompile(`(?is)\bADD\s+(?:COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?("[^"]+"|[a-zA-Z_][a-zA-Z0-9_$]*\b)`)
)

// LintFile memeriksa aturan keamanan dan konsistensi PostgreSQL pada satu file migrasi.
func LintFile(f File) []Issue {
	var issues []Issue

	statements := SplitStatements(f.RawSQL)
	if len(statements) == 0 {
		issues = append(issues, Issue{
			Severity: SeverityError,
			Rule:     "EMPTY_SQL",
			File:     f.Filename,
			Message:  "file SQL tidak memiliki statement yang bermakna",
		})
		return issues
	}

	// File baseline berisi schema dump pg_dump, tidak dikenai aturan destructive/blocking.
	if f.IsBaseline {
		return issues
	}

	createdTables := make(map[string]struct{})
	for _, stmt := range statements {
		clean := StripCommentsAndStrings(stmt)
		for _, m := range reCreateTable.FindAllStringSubmatch(clean, -1) {
			if len(m) > 1 {
				for _, key := range normalizeTableVariants(m[1]) {
					createdTables[key] = struct{}{}
				}
			}
		}
	}

	for _, stmt := range statements {
		clean := StripCommentsAndStrings(stmt)

		// 1. Cek statement yang wajib berjalan di luar transaksi (CONCURRENTLY, VACUUM)
		if f.Directives.Transaction && rePostgresNoTxKeywords.MatchString(clean) {
			issues = append(issues, Issue{
				Severity: SeverityError,
				Rule:     "NO_TX_REQUIRED",
				File:     f.Filename,
				Message:  "statement seperti CONCURRENTLY / VACUUM tidak boleh dijalankan di dalam transaksi; tambahkan '-- migrate:transaction false'",
			})
		}

		// 2. Cek DDL destruktif tanpa -- migrate:allow-destructive
		if f.Directives.AllowDestructive == "" {
			if reDropTable.MatchString(clean) {
				issues = append(issues, Issue{
					Severity: SeverityError,
					Rule:     "DESTRUCTIVE_DROP_TABLE",
					File:     f.Filename,
					Message:  "terdeteksi DROP TABLE tanpa '-- migrate:allow-destructive <alasan>'",
				})
			}

			if hasDropColumn(clean) {
				issues = append(issues, Issue{
					Severity: SeverityError,
					Rule:     "DESTRUCTIVE_DROP_COLUMN",
					File:     f.Filename,
					Message:  "terdeteksi ALTER TABLE ... DROP COLUMN tanpa '-- migrate:allow-destructive <alasan>'",
				})
			}

			if reTruncate.MatchString(clean) {
				issues = append(issues, Issue{
					Severity: SeverityError,
					Rule:     "DESTRUCTIVE_TRUNCATE",
					File:     f.Filename,
					Message:  "terdeteksi TRUNCATE tanpa '-- migrate:allow-destructive <alasan>'",
				})
			}

			if reAlterRename.MatchString(clean) {
				issues = append(issues, Issue{
					Severity: SeverityError,
					Rule:     "BREAKING_RENAME",
					File:     f.Filename,
					Message:  "terdeteksi ALTER TABLE ... RENAME tanpa '-- migrate:allow-destructive <alasan>' (gunakan pola Expand-Contract di multi-repo)",
				})
			}
		}

		// 3. Cek blocking CREATE INDEX pada tabel existing
		if f.Directives.AllowBlockingIndex == "" {
			for _, m := range reCreateIndex.FindAllStringSubmatch(clean, -1) {
				isConcurrent := len(m) > 2 && strings.TrimSpace(m[2]) != ""
				if !isConcurrent && len(m) > 3 {
					tableName := m[3]
					if !isTableInSet(tableName, createdTables) {
						issues = append(issues, Issue{
							Severity: SeverityWarning,
							Rule:     "BLOCKING_INDEX",
							File:     f.Filename,
							Message:  fmt.Sprintf("CREATE INDEX pada tabel existing %s tanpa CONCURRENTLY berisiko mengunci write; gunakan CREATE INDEX CONCURRENTLY (dengan '-- migrate:transaction false') atau '-- migrate:allow-blocking-index <alasan>'", strings.TrimSpace(tableName)),
						})
					}
				}
			}
		}

		// 4. Cek ADD COLUMN ... NOT NULL tanpa DEFAULT
		if hasAddColumnNotNullWithoutDefault(clean) {
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				Rule:     "NOT_NULL_WITHOUT_DEFAULT",
				File:     f.Filename,
				Message:  "terdeteksi ALTER TABLE ... ADD COLUMN ... NOT NULL tanpa DEFAULT; tambahkan DEFAULT atau buat nullable lalu backfill",
			})
		}

		// 5. Cek CREATE INDEX CONCURRENTLY tanpa IF NOT EXISTS
		if rePostgresConcurrentIndex.MatchString(clean) && !rePostgresConcurrentIfNotExists.MatchString(clean) {
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				Rule:     "CONCURRENT_WITHOUT_IF_NOT_EXISTS",
				File:     f.Filename,
				Message:  "CREATE INDEX CONCURRENTLY sebaiknya menyertakan IF NOT EXISTS agar aman diulang jika sebelumnya sempat gagal (invalid index)",
			})
		}
	}

	return issues
}

// LintAll menjalankan LintFile pada seluruh daftar file.
func LintAll(files []File) []Issue {
	var all []Issue
	for _, f := range files {
		all = append(all, LintFile(f)...)
	}
	return all
}

// HasErrors mengembalikan true jika terdapat minimal 1 issue bertipe ERROR,
// atau WARNING saat mode strict aktif.
func HasErrors(issues []Issue, strict bool) bool {
	for _, iss := range issues {
		if iss.Severity == SeverityError {
			return true
		}
		if strict && iss.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

func hasDropColumn(cleanStmt string) bool {
	if !reAlterTable.MatchString(cleanStmt) {
		return false
	}
	matches := reDropClause.FindAllStringSubmatch(cleanStmt, -1)
	for _, m := range matches {
		keyword := strings.ToUpper(strings.TrimSpace(m[1]))
		if keyword == "COLUMN" {
			return true
		}
		if keyword == "" && strings.TrimSpace(m[2]) != "" {
			// Shorthand: ALTER TABLE t DROP [IF EXISTS] <nama_kolom>
			ident := strings.ToUpper(strings.TrimSpace(m[2]))
			switch ident {
			case "CONSTRAINT", "DEFAULT", "NOT", "IDENTITY", "EXPRESSION", "COLUMN":
				continue
			default:
				return true
			}
		}
	}
	return false
}

func hasAddColumnNotNullWithoutDefault(cleanStmt string) bool {
	if !reAlterTable.MatchString(cleanStmt) {
		return false
	}

	// Pecah berdasarkan koma level top-level (di luar tanda kurung) setelah ALTER TABLE ...
	clauses := splitTopLevelComma(cleanStmt)
	for _, clause := range clauses {
		loc := reAddColumn.FindStringIndex(clause)
		if loc == nil {
			continue
		}
		// Ambil sub-klausa mulai dari ADD ...
		addPart := clause[loc[0]:]
		m := reAddColumn.FindStringSubmatch(addPart)
		if len(m) > 1 && strings.EqualFold(m[1], "CONSTRAINT") {
			continue
		}
		// Cek pola "ADD PRIMARY KEY / ADD UNIQUE / ADD CHECK / ADD FOREIGN"
		upperRest := strings.ToUpper(strings.TrimSpace(addPart[3:]))
		if strings.HasPrefix(upperRest, "PRIMARY") ||
			strings.HasPrefix(upperRest, "UNIQUE") ||
			strings.HasPrefix(upperRest, "CHECK") ||
			strings.HasPrefix(upperRest, "FOREIGN") {
			continue
		}

		hasNotNull := regexp.MustCompile(`(?is)\bNOT\s+NULL\b`).MatchString(addPart)
		hasDefault := regexp.MustCompile(`(?is)\bDEFAULT\b`).MatchString(addPart)
		if hasNotNull && !hasDefault {
			return true
		}
	}

	return false
}

func splitTopLevelComma(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func normalizeTableVariants(raw string) []string {
	cleaned := strings.ReplaceAll(raw, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "\t", "")
	cleaned = strings.ReplaceAll(cleaned, "\n", "")
	cleaned = strings.ReplaceAll(cleaned, `"`, "")
	cleaned = strings.ToLower(cleaned)

	variants := []string{cleaned}
	if idx := strings.LastIndex(cleaned, "."); idx >= 0 {
		unqualified := cleaned[idx+1:]
		variants = append(variants, unqualified)
		if strings.HasPrefix(cleaned, "public.") {
			variants = append(variants, unqualified)
		}
	} else {
		variants = append(variants, "public."+cleaned)
	}
	return variants
}

func isTableInSet(raw string, set map[string]struct{}) bool {
	for _, v := range normalizeTableVariants(raw) {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}
