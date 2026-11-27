package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	// MigrationFileRegex mencocokkan YYYYMMDDHHmmss_<service>_<deskripsi>.sql
	MigrationFileRegex = regexp.MustCompile(`^(\d{14})_([a-z][a-z0-9-]*)_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)
	// BaselineFileRegex mencocokkan 00000000000000_<deskripsi>.sql
	BaselineFileRegex = regexp.MustCompile(`^(0{14})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)
	// ServiceNameRegex memvalidasi nama service pada CLI create maupun filename
	ServiceNameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// DescriptionRegex memvalidasi deskripsi snake_case
	DescriptionRegex = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)
)

// Directives menyimpan metadata opsional pada header komentar file migrasi SQL.
type Directives struct {
	Service            string
	Ticket             string
	Transaction        bool
	LockTimeout        string
	StatementTimeout   string
	AllowDestructive   string
	AllowBlockingIndex string
}

// File merepresentasikan satu file migrasi atau baseline yang telah diparsing.
type File struct {
	Version     string
	Filename    string
	Path        string
	Service     string
	Description string
	IsBaseline  bool
	RawSQL      string
	Checksum    string
	Directives  Directives
}

// NormalizeLineEndings menyamakan CRLF/CR menjadi LF agar checksum identik di Windows dan Linux.
func NormalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// ComputeChecksum menghitung SHA-256 hex dari SQL yang sudah dinormalisasi line ending-nya.
func ComputeChecksum(normalizedSQL string) string {
	sum := sha256.Sum256([]byte(normalizedSQL))
	return hex.EncodeToString(sum[:])
}

// ParseDirectives membaca header komentar "-- migrate:<key> <value>" di awal file sebelum statement SQL pertama.
func ParseDirectives(normalizedSQL string) (Directives, error) {
	dirs := Directives{
		Transaction: true,
	}

	lines := strings.Split(normalizedSQL, "\n")
	inBlockComment := false

	for i, rawLine := range lines {
		lineNum := i + 1
		line := strings.TrimSpace(rawLine)

		if inBlockComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				inBlockComment = false
				rest := strings.TrimSpace(line[idx+2:])
				if rest == "" {
					continue
				}
				line = rest
			} else {
				continue
			}
		}

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/*") {
			if idx := strings.Index(line[2:], "*/"); idx >= 0 {
				rest := strings.TrimSpace(line[2+idx+2:])
				if rest == "" {
					continue
				}
				// Jika ada SQL setelah */ di baris yang sama, header komentar selesai
				if !strings.HasPrefix(rest, "--") {
					break
				}
				line = rest
			} else {
				inBlockComment = true
				continue
			}
		}

		if !strings.HasPrefix(line, "--") {
			// Statement SQL pertama telah dimulai
			break
		}

		commentBody := strings.TrimSpace(strings.TrimPrefix(line, "--"))
		if !strings.HasPrefix(commentBody, "migrate:") {
			continue
		}

		directiveText := strings.TrimSpace(strings.TrimPrefix(commentBody, "migrate:"))
		if directiveText == "" {
			return dirs, fmt.Errorf("baris %d: directive '-- migrate:' kosong", lineNum)
		}

		key, val, hasVal := cutWhitespace(directiveText)
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)

		switch key {
		case "service":
			if !hasVal || val == "" {
				return dirs, fmt.Errorf("baris %d: '-- migrate:service' membutuhkan nilai", lineNum)
			}
			dirs.Service = val
		case "ticket":
			if !hasVal || val == "" {
				return dirs, fmt.Errorf("baris %d: '-- migrate:ticket' membutuhkan nilai", lineNum)
			}
			dirs.Ticket = val
		case "transaction":
			if !hasVal || (val != "true" && val != "false") {
				return dirs, fmt.Errorf("baris %d: '-- migrate:transaction' harus bernilai 'true' atau 'false', dapat %q", lineNum, val)
			}
			b, _ := strconv.ParseBool(val)
			dirs.Transaction = b
		case "lock-timeout":
			if !hasVal || val == "" {
				return dirs, fmt.Errorf("baris %d: '-- migrate:lock-timeout' membutuhkan durasi", lineNum)
			}
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return dirs, fmt.Errorf("baris %d: '-- migrate:lock-timeout' %q tidak valid", lineNum, val)
			}
			dirs.LockTimeout = val
		case "statement-timeout":
			if !hasVal || val == "" {
				return dirs, fmt.Errorf("baris %d: '-- migrate:statement-timeout' membutuhkan durasi", lineNum)
			}
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return dirs, fmt.Errorf("baris %d: '-- migrate:statement-timeout' %q tidak valid", lineNum, val)
			}
			dirs.StatementTimeout = val
		case "allow-destructive":
			if !hasVal || val == "" {
				return dirs, fmt.Errorf("baris %d: '-- migrate:allow-destructive' wajib menyertakan alasan eksplisit", lineNum)
			}
			dirs.AllowDestructive = val
		case "allow-blocking-index":
			if !hasVal || val == "" {
				return dirs, fmt.Errorf("baris %d: '-- migrate:allow-blocking-index' wajib menyertakan alasan eksplisit", lineNum)
			}
			dirs.AllowBlockingIndex = val
		default:
			return dirs, fmt.Errorf("baris %d: directive tidak dikenal 'migrate:%s'", lineNum, key)
		}
	}

	return dirs, nil
}

func cutWhitespace(s string) (head, tail string, ok bool) {
	for i, r := range s {
		if unicode.IsSpace(r) {
			return s[:i], strings.TrimSpace(s[i:]), true
		}
	}
	return s, "", false
}

// ParseFile membaca dan mem-parsing satu file migrasi atau baseline dari disk.
func ParseFile(filePath string, isBaseline bool) (File, error) {
	filename := filepath.Base(filePath)
	var version, fileService, desc string

	if isBaseline {
		m := BaselineFileRegex.FindStringSubmatch(filename)
		if m == nil {
			return File{}, fmt.Errorf("nama file baseline %q tidak valid (harus memenuhi pola 00000000000000_<deskripsi>.sql)", filename)
		}
		version = m[1]
		fileService = "baseline"
		desc = m[2]
	} else {
		m := MigrationFileRegex.FindStringSubmatch(filename)
		if m == nil {
			return File{}, fmt.Errorf("nama file migrasi %q tidak valid (harus memenuhi pola YYYYMMDDHHmmss_<service>_<deskripsi>.sql)", filename)
		}
		version = m[1]
		if version == "00000000000000" {
			return File{}, fmt.Errorf("file migrasi %q tidak boleh menggunakan versi 00000000000000 di folder migrations/", filename)
		}
		fileService = m[2]
		desc = m[3]
	}

	rawBytes, err := os.ReadFile(filePath)
	if err != nil {
		return File{}, fmt.Errorf("gagal membaca file %q: %w", filePath, err)
	}

	normalized := NormalizeLineEndings(string(rawBytes))
	dirs, err := ParseDirectives(normalized)
	if err != nil {
		return File{}, fmt.Errorf("error directive pada %s: %w", filename, err)
	}

	service := fileService
	if dirs.Service != "" {
		service = dirs.Service
	}

	return File{
		Version:     version,
		Filename:    filename,
		Path:        filePath,
		Service:     service,
		Description: desc,
		IsBaseline:  isBaseline,
		RawSQL:      normalized,
		Checksum:    ComputeChecksum(normalized),
		Directives:  dirs,
	}, nil
}

// LoadDatabaseFiles memuat file baseline (maksimal 1) dan daftar file migrasi terurut ascending dari baseDir.
func LoadDatabaseFiles(baseDir string) (baseline *File, migrations []File, err error) {
	baselineDir := filepath.Join(baseDir, "baseline")
	if info, statErr := os.Stat(baselineDir); statErr == nil && info.IsDir() {
		entries, readErr := os.ReadDir(baselineDir)
		if readErr != nil {
			return nil, nil, fmt.Errorf("gagal membaca folder baseline %q: %w", baselineDir, readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() || isIgnorableAuxFile(entry.Name()) {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".sql") {
				return nil, nil, fmt.Errorf("file tidak dikenal %q di folder baseline (hanya mendukung .sql)", entry.Name())
			}
			f, parseErr := ParseFile(filepath.Join(baselineDir, entry.Name()), true)
			if parseErr != nil {
				return nil, nil, parseErr
			}
			if baseline != nil {
				return nil, nil, fmt.Errorf("folder baseline %q hanya boleh berisi maksimal 1 file .sql (ditemukan %s dan %s)", baselineDir, baseline.Filename, f.Filename)
			}
			fileCopy := f
			baseline = &fileCopy
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, nil, fmt.Errorf("gagal memeriksa folder baseline %q: %w", baselineDir, statErr)
	}

	migrationsDir := filepath.Join(baseDir, "migrations")
	info, statErr := os.Stat(migrationsDir)
	if os.IsNotExist(statErr) {
		return baseline, nil, nil
	}
	if statErr != nil {
		return nil, nil, fmt.Errorf("gagal memeriksa folder migrations %q: %w", migrationsDir, statErr)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%q bukan direktori", migrationsDir)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("gagal membaca folder migrations %q: %w", migrationsDir, err)
	}

	seenVersions := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || isIgnorableAuxFile(entry.Name()) {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".sql") {
			return nil, nil, fmt.Errorf("file tidak dikenal %q di folder migrations (hanya mendukung .sql)", entry.Name())
		}
		f, parseErr := ParseFile(filepath.Join(migrationsDir, entry.Name()), false)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if prevFile, dup := seenVersions[f.Version]; dup {
			return nil, nil, fmt.Errorf("duplikat versi %s antara %s dan %s", f.Version, prevFile, f.Filename)
		}
		seenVersions[f.Version] = f.Filename
		migrations = append(migrations, f)
	}

	sort.Slice(migrations, func(i, j int) bool {
		if migrations[i].Version == migrations[j].Version {
			return migrations[i].Filename < migrations[j].Filename
		}
		return migrations[i].Version < migrations[j].Version
	})

	return baseline, migrations, nil
}

func isIgnorableAuxFile(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".gitkeep" || lower == "readme.md" || lower == "readme.txt" || lower == ".ds_store"
}

// SplitStatements memecah skrip SQL menjadi daftar statement PostgreSQL yang valid.
// Mendukung string literal ('...'), escape string (E'...'), quoted identifier ("..."),
// dollar-quoting ($$...$$, $tag$...$tag$), komentar baris (--), dan nested block comment (/* ... */).
func SplitStatements(sql string) []string {
	var statements []string
	var buf strings.Builder

	n := len(sql)
	i := 0

	for i < n {
		ch := sql[i]

		// 1. Line comment: -- ... \n
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			start := i
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			buf.WriteString(sql[start:i])
			continue
		}

		// 2. Nested block comment: /* ... /* ... */ ... */
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			start := i
			i += 2
			depth := 1
			for i < n && depth > 0 {
				if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					depth--
					i += 2
					continue
				}
				i++
			}
			buf.WriteString(sql[start:i])
			continue
		}

		// 3. Escape string literal: E'...' atau e'...'
		if (ch == 'E' || ch == 'e') && i+1 < n && sql[i+1] == '\'' && (i == 0 || !isIdentChar(sql[i-1])) {
			start := i
			i += 2
			for i < n {
				if sql[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			buf.WriteString(sql[start:i])
			continue
		}

		// 4. Standard single-quoted string: '...'
		if ch == '\'' {
			start := i
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			buf.WriteString(sql[start:i])
			continue
		}

		// 5. Double-quoted identifier: "..."
		if ch == '"' {
			start := i
			i++
			for i < n {
				if sql[i] == '"' {
					if i+1 < n && sql[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			buf.WriteString(sql[start:i])
			continue
		}

		// 6. Dollar-quoted string: $$...$$ atau $tag$...$tag$
		if ch == '$' {
			if tag, ok := parseDollarTag(sql, i); ok {
				start := i
				i += len(tag)
				endIdx := strings.Index(sql[i:], tag)
				if endIdx >= 0 {
					i += endIdx + len(tag)
				} else {
					i = n
				}
				buf.WriteString(sql[start:i])
				continue
			}
		}

		// 7. Statement delimiter ';'
		if ch == ';' {
			stmt := strings.TrimSpace(buf.String())
			if hasMeaningfulSQL(stmt) {
				statements = append(statements, stmt)
			}
			buf.Reset()
			i++
			continue
		}

		buf.WriteByte(ch)
		i++
	}

	if tail := strings.TrimSpace(buf.String()); hasMeaningfulSQL(tail) {
		statements = append(statements, tail)
	}

	return statements
}

// StripCommentsAndStrings menghapus komentar SQL serta mengganti isi literal string ('...', E'...', $$...$$)
// dengan placeholder kosong '' agar linter tidak mengalami false positive.
func StripCommentsAndStrings(sql string) string {
	var buf strings.Builder
	n := len(sql)
	i := 0

	for i < n {
		ch := sql[i]

		// Line comment -> spasi
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			buf.WriteByte(' ')
			continue
		}

		// Nested block comment -> spasi
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			depth := 1
			for i < n && depth > 0 {
				if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					depth--
					i += 2
					continue
				}
				i++
			}
			buf.WriteByte(' ')
			continue
		}

		// Escape string E'...' -> ''
		if (ch == 'E' || ch == 'e') && i+1 < n && sql[i+1] == '\'' && (i == 0 || !isIdentChar(sql[i-1])) {
			i += 2
			for i < n {
				if sql[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			buf.WriteString("''")
			continue
		}

		// Single-quoted string '...' -> ''
		if ch == '\'' {
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			buf.WriteString("''")
			continue
		}

		// Double-quoted identifier "..." dipertahankan
		if ch == '"' {
			start := i
			i++
			for i < n {
				if sql[i] == '"' {
					if i+1 < n && sql[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			buf.WriteString(sql[start:i])
			continue
		}

		// Dollar-quoted string $$...$$ -> ''
		if ch == '$' {
			if tag, ok := parseDollarTag(sql, i); ok {
				i += len(tag)
				endIdx := strings.Index(sql[i:], tag)
				if endIdx >= 0 {
					i += endIdx + len(tag)
				} else {
					i = n
				}
				buf.WriteString("''")
				continue
			}
		}

		buf.WriteByte(ch)
		i++
	}

	return buf.String()
}

func hasMeaningfulSQL(stmt string) bool {
	stripped := strings.TrimSpace(StripCommentsAndStrings(stmt))
	return stripped != ""
}

func parseDollarTag(sql string, pos int) (string, bool) {
	if pos >= len(sql) || sql[pos] != '$' {
		return "", false
	}
	j := pos + 1
	if j < len(sql) && sql[j] == '$' {
		return "$$", true
	}
	if j >= len(sql) || !isTagStartChar(sql[j]) {
		return "", false
	}
	j++
	for j < len(sql) && isIdentChar(sql[j]) {
		j++
	}
	if j < len(sql) && sql[j] == '$' {
		return sql[pos : j+1], true
	}
	return "", false
}

func isTagStartChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isIdentChar(b byte) bool {
	return isTagStartChar(b) || (b >= '0' && b <= '9')
}
