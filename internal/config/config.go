package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultTrackingSchema  = "public"
	DefaultTrackingTable   = "schema_migrations"
	DefaultLockTimeout     = "5s"
	DefaultStmtTimeout     = "60s"
	DefaultLockWaitTimeout = "120s"
)

var (
	identRegex  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	dbNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// Config merepresentasikan isi file databases.yaml.
type Config struct {
	Version   int              `yaml:"version"`
	Defaults  DefaultsConfig   `yaml:"defaults"`
	Databases []DatabaseConfig `yaml:"databases"`
	BaseDir   string           `yaml:"-"`
}

// DefaultsConfig menyimpan konfigurasi bawaan untuk semua database.
type DefaultsConfig struct {
	TrackingSchema   string `yaml:"tracking_schema"`
	TrackingTable    string `yaml:"tracking_table"`
	LockTimeout      string `yaml:"lock_timeout"`
	StatementTimeout string `yaml:"statement_timeout"`
	LockWaitTimeout  string `yaml:"lock_wait_timeout"`
}

// DatabaseConfig mendefinisikan satu target database dalam server PostgreSQL.
type DatabaseConfig struct {
	Name        string `yaml:"name"`
	Database    string `yaml:"database"`
	DatabaseEnv string `yaml:"database_env"`
	DSNEnv      string `yaml:"dsn_env"`
	Dir         string `yaml:"dir"`
	Description string `yaml:"description"`
	Enabled     bool   `yaml:"enabled"`
}

// ResolvedDatabase menyimpan konfigurasi database beserta DSN dan nama DB aktual hasil resolusi env.
type ResolvedDatabase struct {
	Config       DatabaseConfig
	ActualDBName string
	DSN          string
}

type rawDatabaseConfig struct {
	Name        string `yaml:"name"`
	Database    string `yaml:"database"`
	DatabaseEnv string `yaml:"database_env"`
	DSNEnv      string `yaml:"dsn_env"`
	Dir         string `yaml:"dir"`
	Description string `yaml:"description"`
	Enabled     *bool  `yaml:"enabled"`
}

// UnmarshalYAML memastikan field enabled bernilai true secara default jika tidak dicantumkan.
func (d *DatabaseConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw rawDatabaseConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	d.Name = raw.Name
	d.Database = raw.Database
	d.DatabaseEnv = raw.DatabaseEnv
	d.DSNEnv = raw.DSNEnv
	d.Dir = raw.Dir
	d.Description = raw.Description
	if raw.Enabled == nil {
		d.Enabled = true
	} else {
		d.Enabled = *raw.Enabled
	}
	return nil
}

// Load membaca, menerapkan nilai bawaan, dan memvalidasi file konfigurasi databases.yaml.
func Load(configPath string) (*Config, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, fmt.Errorf("path konfigurasi tidak boleh kosong")
	}

	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca path absolut konfigurasi %q: %w", configPath, err)
	}

	data, err := os.ReadFile(absConfigPath)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca file konfigurasi %q: %w", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("gagal mengurai YAML %q: %w", configPath, err)
	}

	cfg.BaseDir = filepath.Dir(absConfigPath)
	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if strings.TrimSpace(c.Defaults.TrackingSchema) == "" {
		c.Defaults.TrackingSchema = DefaultTrackingSchema
	}
	if strings.TrimSpace(c.Defaults.TrackingTable) == "" {
		c.Defaults.TrackingTable = DefaultTrackingTable
	}
	if strings.TrimSpace(c.Defaults.LockTimeout) == "" {
		c.Defaults.LockTimeout = DefaultLockTimeout
	}
	if strings.TrimSpace(c.Defaults.StatementTimeout) == "" {
		c.Defaults.StatementTimeout = DefaultStmtTimeout
	}
	if strings.TrimSpace(c.Defaults.LockWaitTimeout) == "" {
		c.Defaults.LockWaitTimeout = DefaultLockWaitTimeout
	}
}

func (c *Config) validate() error {
	if !identRegex.MatchString(c.Defaults.TrackingSchema) {
		return fmt.Errorf("tracking_schema %q tidak valid (harus memenuhi pola %s)", c.Defaults.TrackingSchema, identRegex.String())
	}
	if !identRegex.MatchString(c.Defaults.TrackingTable) {
		return fmt.Errorf("tracking_table %q tidak valid (harus memenuhi pola %s)", c.Defaults.TrackingTable, identRegex.String())
	}

	if err := validatePositiveDuration("defaults.lock_timeout", c.Defaults.LockTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("defaults.statement_timeout", c.Defaults.StatementTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("defaults.lock_wait_timeout", c.Defaults.LockWaitTimeout); err != nil {
		return err
	}

	if len(c.Databases) == 0 {
		return fmt.Errorf("daftar databases tidak boleh kosong")
	}

	seen := make(map[string]struct{}, len(c.Databases))
	for i, db := range c.Databases {
		name := strings.TrimSpace(db.Name)
		if name == "" {
			return fmt.Errorf("databases[%d].name tidak boleh kosong", i)
		}
		if strings.EqualFold(name, "all") {
			return fmt.Errorf("databases[%d].name tidak boleh bernilai 'all' karena merupakan keyword CLI", i)
		}
		if !dbNameRegex.MatchString(name) {
			return fmt.Errorf("databases[%d].name %q tidak valid (hanya boleh huruf, angka, '-' dan '_')", i, name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplikat nama database %q pada databases.yaml", name)
		}
		seen[name] = struct{}{}

		if strings.TrimSpace(db.Dir) == "" {
			return fmt.Errorf("databases[%d] (%s): field 'dir' wajib diisi", i, name)
		}
		if strings.TrimSpace(db.Database) == "" && strings.TrimSpace(db.DSNEnv) == "" {
			return fmt.Errorf("databases[%d] (%s): minimal 'database' atau 'dsn_env' harus diisi", i, name)
		}
	}

	return nil
}

func validatePositiveDuration(field, raw string) error {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s %q bukan durasi yang valid: %w", field, raw, err)
	}
	if d <= 0 {
		return fmt.Errorf("%s %q harus lebih besar dari 0", field, raw)
	}
	return nil
}

// ResolveDir mengembalikan path direktori migrasi absolut untuk suatu database.
func (c *Config) ResolveDir(db DatabaseConfig) string {
	if filepath.IsAbs(db.Dir) || c.BaseDir == "" {
		return filepath.Clean(db.Dir)
	}
	return filepath.Clean(filepath.Join(c.BaseDir, db.Dir))
}

// LockWaitDuration mengembalikan nilai lock_wait_timeout sebagai time.Duration.
func (c *Config) LockWaitDuration() (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(c.Defaults.LockWaitTimeout))
}

// SelectDatabases memilih daftar database aktif berdasarkan selector ("all", "", atau comma-separated).
func (c *Config) SelectDatabases(selector string) ([]DatabaseConfig, error) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" || strings.EqualFold(trimmed, "all") {
		var selected []DatabaseConfig
		for _, db := range c.Databases {
			if db.Enabled {
				selected = append(selected, db)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("tidak ada database aktif (enabled: true) di konfigurasi")
		}
		return selected, nil
	}

	byName := make(map[string]DatabaseConfig, len(c.Databases))
	for _, db := range c.Databases {
		byName[db.Name] = db
	}

	parts := strings.Split(trimmed, ",")
	seen := make(map[string]struct{}, len(parts))
	var selected []DatabaseConfig

	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("selector database %q mengandung entri kosong", selector)
		}
		if strings.EqualFold(name, "all") {
			return nil, fmt.Errorf("selector 'all' tidak boleh digabung dengan nama database lain")
		}
		db, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("database %q tidak terdaftar di databases.yaml", name)
		}
		if !db.Enabled {
			return nil, fmt.Errorf("database %q berstatus disabled (enabled: false)", name)
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, db)
	}

	return selected, nil
}

// ResolveDSN membentuk PostgreSQL connection string dan nama database aktual dari environment variables.
func ResolveDSN(db DatabaseConfig, lookupEnv func(string) (string, bool)) (dsn string, actualDBName string, err error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	// 1. Cek override full DSN (misalnya jika suatu DB berada di server terpisah)
	if envKey := strings.TrimSpace(db.DSNEnv); envKey != "" {
		if rawDSN, ok := lookupEnv(envKey); ok && strings.TrimSpace(rawDSN) != "" {
			trimmedDSN := strings.TrimSpace(rawDSN)
			parsedName := extractDBNameFromDSN(trimmedDSN, db.Database, db.Name)
			return trimmedDSN, parsedName, nil
		}
	}

	// 2. Resolusi nama database aktual (bisa dioverride per environment via database_env)
	actualDBName = strings.TrimSpace(db.Database)
	if dbEnvKey := strings.TrimSpace(db.DatabaseEnv); dbEnvKey != "" {
		if envDBName, ok := lookupEnv(dbEnvKey); ok && strings.TrimSpace(envDBName) != "" {
			actualDBName = strings.TrimSpace(envDBName)
		}
	}
	if actualDBName == "" {
		return "", "", fmt.Errorf("database %q tidak memiliki nama database maupun nilai pada env %q", db.Name, db.DSNEnv)
	}

	// 3. Bangun DSN dari variabel server PostgreSQL (prioritas: POSTGRES_* -> PG_* -> DB_*)
	host := getFirstTrimmedEnv(lookupEnv, "POSTGRES_HOST", "PG_HOST", "DB_HOST")
	if host == "" {
		return "", "", fmt.Errorf("variabel lingkungan POSTGRES_HOST / PG_HOST / DB_HOST wajib diisi untuk koneksi database %q", db.Name)
	}

	user := getFirstTrimmedEnv(lookupEnv, "POSTGRES_USER", "PG_USER", "DB_USER")
	if user == "" {
		return "", "", fmt.Errorf("variabel lingkungan POSTGRES_USER / PG_USER / DB_USER wajib diisi untuk koneksi database %q", db.Name)
	}

	port := getFirstTrimmedEnv(lookupEnv, "POSTGRES_PORT", "PG_PORT", "DB_PORT")
	if port == "" {
		port = "5432"
	} else {
		p, convErr := strconv.Atoi(port)
		if convErr != nil || p < 1 || p > 65535 {
			return "", "", fmt.Errorf("variabel lingkungan POSTGRES_PORT/PG_PORT/DB_PORT %q tidak valid", port)
		}
	}

	password, hasPassword := firstPresentEnv(lookupEnv, "POSTGRES_PASSWORD", "PG_PASSWORD", "DB_PASSWORD")
	var userInfo *url.Userinfo
	if hasPassword && password != "" {
		userInfo = url.UserPassword(user, password)
	} else {
		userInfo = url.User(user)
	}

	sslMode := getFirstTrimmedEnv(lookupEnv, "POSTGRES_SSLMODE", "PG_SSLMODE", "DB_SSLMODE")
	if sslMode == "" {
		sslMode = "disable"
	}

	query := url.Values{}
	query.Set("sslmode", sslMode)

	u := &url.URL{
		Scheme:   "postgres",
		User:     userInfo,
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + actualDBName,
		RawQuery: query.Encode(),
	}

	return u.String(), actualDBName, nil
}

// ResolveAll meresolusi DSN untuk sekumpulan database terpilih.
func ResolveAll(dbs []DatabaseConfig, lookupEnv func(string) (string, bool)) ([]ResolvedDatabase, error) {
	out := make([]ResolvedDatabase, 0, len(dbs))
	for _, db := range dbs {
		dsn, actualName, err := ResolveDSN(db, lookupEnv)
		if err != nil {
			return nil, err
		}
		out = append(out, ResolvedDatabase{
			Config:       db,
			ActualDBName: actualName,
			DSN:          dsn,
		})
	}
	return out, nil
}

func getFirstTrimmedEnv(lookupEnv func(string) (string, bool), keys ...string) string {
	for _, key := range keys {
		if val, ok := lookupEnv(key); ok {
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func firstPresentEnv(lookupEnv func(string) (string, bool), keys ...string) (string, bool) {
	for _, key := range keys {
		if val, ok := lookupEnv(key); ok {
			return val, true
		}
	}
	return "", false
}

func extractDBNameFromDSN(rawDSN, fallbackDB, fallbackName string) string {
	if u, err := url.Parse(rawDSN); err == nil {
		dbName := strings.TrimPrefix(u.Path, "/")
		if dbName != "" {
			return dbName
		}
	}
	if strings.TrimSpace(fallbackDB) != "" {
		return strings.TrimSpace(fallbackDB)
	}
	return fallbackName
}
