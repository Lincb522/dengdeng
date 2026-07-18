package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig   `yaml:"server"`
	Database      DatabaseConfig `yaml:"database"`
	JWT           JWTConfig      `yaml:"jwt"`
	Admin         AdminConfig    `yaml:"admin"`
	Site          SiteConfig     `yaml:"site"`
	SMTP          SMTPConfig     `yaml:"smtp"`
	Backup        BackupConfig   `yaml:"backup"`
	Update        UpdateConfig   `yaml:"update"`
	OAuth         OAuthConfig    `yaml:"oauth"`
	Proxy         ProxyConfig    `yaml:"proxy"`
	EncryptionKey string         `yaml:"encryption_key"`
}

type ServerConfig struct {
	Host           string   `yaml:"host"`
	Port           int      `yaml:"port"`
	Mode           string   `yaml:"mode"` // debug | release
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// DatabaseConfig selects the storage backend. Driver "sqlite" needs only a
// file path (zero-dependency local run); "postgres" uses the DSN fields.
type DatabaseConfig struct {
	Driver   string `yaml:"driver"`
	Path     string `yaml:"path"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

type JWTConfig struct {
	Secret     string `yaml:"secret"`
	ExpireHour int    `yaml:"expire_hour"`
}

type AdminConfig struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
}

type SiteConfig struct {
	Name string `yaml:"name"`
	// PublicURL is the externally reachable HTTPS origin, used for payment
	// webhooks and browser returns. It is intentionally explicit so a forged
	// Host header can never redirect a merchant callback.
	PublicURL        string `yaml:"public_url"`
	AllowRegister    bool   `yaml:"allow_register"`
	InitBalanceMicro int64  `yaml:"init_balance_micro"`
}

// SMTPConfig follows the same SMTP_* environment contract used by the
// existing CertVault project, so deployment credentials can be shared rather
// than duplicated in application code.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Secure   bool   `yaml:"secure"`
	User     string `yaml:"user"`
	Pass     string `yaml:"pass"`
	FromName string `yaml:"from_name"`
	From     string `yaml:"from"`
}

// BackupConfig controls where server-side database snapshots are stored. The
// directory is never served directly; an authenticated administrator must use
// the backup API to retrieve a snapshot.
type BackupConfig struct {
	Directory      string `yaml:"directory"`
	AutoEnabled    bool   `yaml:"auto_enabled"`
	IntervalHours  int    `yaml:"interval_hours"`
	RetentionDays  int    `yaml:"retention_days"`
	RetentionCount int    `yaml:"retention_count"`
}

// UpdateConfig exposes the operator-installed updater to the administration
// console. The privileged helper still reads its repository and branch from a
// root-owned file; these values are informational and never become shell
// arguments supplied by an HTTP request. Polkit only permits the application
// account to start the fixed updater unit.
type UpdateConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Repository     string `yaml:"repository"`
	Branch         string `yaml:"branch"`
	StateDirectory string `yaml:"state_directory"`
}

// OAuthConfig holds the optional client registration overrides used by the
// upstream-account "sign in with OAuth" flow. RedirectURL must be the exact
// callback URL registered with the provider. When it is empty, development on
// localhost uses the current local address automatically.
type OAuthConfig struct {
	OpenAI    OAuthProviderConfig `yaml:"openai"`
	Anthropic OAuthProviderConfig `yaml:"anthropic"`
	Gemini    OAuthProviderConfig `yaml:"gemini"`
	Grok      OAuthProviderConfig `yaml:"grok"`
}

type OAuthProviderConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	AuthorizeURL string `yaml:"authorize_url"`
	TokenURL     string `yaml:"token_url"`
	Scope        string `yaml:"scope"`
	RedirectURL  string `yaml:"redirect_url"`
}

// ProxyConfig controls outbound requests to model providers and OAuth token
// endpoints. It is deliberately separate from the public server listener.
// The proxy must be an HTTP(S) CONNECT proxy URL.
type ProxyConfig struct {
	URL     string `yaml:"url"`
	NoProxy string `yaml:"no_proxy"`
}

func Default() *Config {
	return &Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 9100, Mode: "release"},
		Database: DatabaseConfig{Driver: "sqlite", Path: "data/dengdeng.db", Host: "localhost", Port: 5432, User: "dengdeng", DBName: "dengdeng", SSLMode: "disable"},
		JWT:      JWTConfig{ExpireHour: 72},
		Admin:    AdminConfig{Email: "admin@dengdeng.local", Password: ""},
		Site:     SiteConfig{Name: "DengDeng AI · 蹬蹬ai", AllowRegister: true, InitBalanceMicro: 0},
		SMTP:     SMTPConfig{Host: "smtp.qq.com", Port: 465, Secure: true, FromName: "DengDeng AI"},
		Backup: BackupConfig{
			AutoEnabled:    true,
			IntervalHours:  24,
			RetentionDays:  30,
			RetentionCount: 30,
		},
		Update: UpdateConfig{
			Repository:     "https://github.com/Lincb522/dengdeng.git",
			Branch:         "main",
			StateDirectory: "/var/lib/dengdeng/update",
		},
	}
}

// Load reads config from an optional YAML file, then applies environment
// variable overrides (env wins, which suits container deployment).
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	applyEnv(cfg)
	if cfg.JWT.Secret == "" {
		return nil, fmt.Errorf("jwt.secret is required (set JWT_SECRET env or jwt.secret in config)")
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	envStr := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	envInt := func(key string, dst *int) {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	envStr("SERVER_HOST", &cfg.Server.Host)
	envInt("SERVER_PORT", &cfg.Server.Port)
	envStr("SERVER_MODE", &cfg.Server.Mode)
	if raw := os.Getenv("SERVER_TRUSTED_PROXIES"); raw != "" {
		parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
		cfg.Server.TrustedProxies = parts
	}

	envStr("DATABASE_DRIVER", &cfg.Database.Driver)
	envStr("DATABASE_PATH", &cfg.Database.Path)
	envStr("DATABASE_HOST", &cfg.Database.Host)
	envInt("DATABASE_PORT", &cfg.Database.Port)
	envStr("DATABASE_USER", &cfg.Database.User)
	envStr("DATABASE_PASSWORD", &cfg.Database.Password)
	envStr("DATABASE_DBNAME", &cfg.Database.DBName)
	envStr("DATABASE_SSLMODE", &cfg.Database.SSLMode)

	envStr("JWT_SECRET", &cfg.JWT.Secret)
	envInt("JWT_EXPIRE_HOUR", &cfg.JWT.ExpireHour)

	envStr("SMTP_HOST", &cfg.SMTP.Host)
	envInt("SMTP_PORT", &cfg.SMTP.Port)
	if v := os.Getenv("SMTP_SECURE"); v != "" {
		cfg.SMTP.Secure = v == "true" || v == "1"
	}
	envStr("SMTP_USER", &cfg.SMTP.User)
	envStr("SMTP_PASS", &cfg.SMTP.Pass)
	envStr("SMTP_FROM_NAME", &cfg.SMTP.FromName)
	envStr("SMTP_FROM", &cfg.SMTP.From)
	envStr("BACKUP_DIRECTORY", &cfg.Backup.Directory)
	if v := os.Getenv("BACKUP_AUTO_ENABLED"); v != "" {
		cfg.Backup.AutoEnabled = v == "true" || v == "1"
	}
	envInt("BACKUP_INTERVAL_HOURS", &cfg.Backup.IntervalHours)
	envInt("BACKUP_RETENTION_DAYS", &cfg.Backup.RetentionDays)
	envInt("BACKUP_RETENTION_COUNT", &cfg.Backup.RetentionCount)
	if v := os.Getenv("UPDATE_ENABLED"); v != "" {
		cfg.Update.Enabled = v == "true" || v == "1"
	}
	envStr("UPDATE_REPOSITORY", &cfg.Update.Repository)
	envStr("UPDATE_BRANCH", &cfg.Update.Branch)
	envStr("UPDATE_STATE_DIRECTORY", &cfg.Update.StateDirectory)

	envStr("ENCRYPTION_KEY", &cfg.EncryptionKey)
	envStr("PROXY_URL", &cfg.Proxy.URL)
	envStr("NO_PROXY", &cfg.Proxy.NoProxy)

	envStr("ADMIN_EMAIL", &cfg.Admin.Email)
	envStr("ADMIN_PASSWORD", &cfg.Admin.Password)

	envStr("SITE_NAME", &cfg.Site.Name)
	envStr("SITE_PUBLIC_URL", &cfg.Site.PublicURL)
	if v := os.Getenv("SITE_ALLOW_REGISTER"); v != "" {
		cfg.Site.AllowRegister = v == "true" || v == "1"
	}

	envStr("OAUTH_OPENAI_CLIENT_ID", &cfg.OAuth.OpenAI.ClientID)
	envStr("OAUTH_OPENAI_CLIENT_SECRET", &cfg.OAuth.OpenAI.ClientSecret)
	envStr("OAUTH_OPENAI_AUTHORIZE_URL", &cfg.OAuth.OpenAI.AuthorizeURL)
	envStr("OAUTH_OPENAI_TOKEN_URL", &cfg.OAuth.OpenAI.TokenURL)
	envStr("OAUTH_OPENAI_SCOPE", &cfg.OAuth.OpenAI.Scope)
	envStr("OAUTH_OPENAI_REDIRECT_URL", &cfg.OAuth.OpenAI.RedirectURL)

	envStr("OAUTH_ANTHROPIC_CLIENT_ID", &cfg.OAuth.Anthropic.ClientID)
	envStr("OAUTH_ANTHROPIC_CLIENT_SECRET", &cfg.OAuth.Anthropic.ClientSecret)
	envStr("OAUTH_ANTHROPIC_AUTHORIZE_URL", &cfg.OAuth.Anthropic.AuthorizeURL)
	envStr("OAUTH_ANTHROPIC_TOKEN_URL", &cfg.OAuth.Anthropic.TokenURL)
	envStr("OAUTH_ANTHROPIC_SCOPE", &cfg.OAuth.Anthropic.Scope)
	envStr("OAUTH_ANTHROPIC_REDIRECT_URL", &cfg.OAuth.Anthropic.RedirectURL)

	envStr("OAUTH_GEMINI_CLIENT_ID", &cfg.OAuth.Gemini.ClientID)
	envStr("OAUTH_GEMINI_CLIENT_SECRET", &cfg.OAuth.Gemini.ClientSecret)
	envStr("OAUTH_GEMINI_AUTHORIZE_URL", &cfg.OAuth.Gemini.AuthorizeURL)
	envStr("OAUTH_GEMINI_TOKEN_URL", &cfg.OAuth.Gemini.TokenURL)
	envStr("OAUTH_GEMINI_SCOPE", &cfg.OAuth.Gemini.Scope)
	envStr("OAUTH_GEMINI_REDIRECT_URL", &cfg.OAuth.Gemini.RedirectURL)

	// xAI / Grok OAuth follows the public XAI_OAUTH_* contract used by the
	// Grok CLI, so operators can reuse the same environment they already set.
	envStr("XAI_OAUTH_CLIENT_ID", &cfg.OAuth.Grok.ClientID)
	envStr("XAI_OAUTH_CLIENT_SECRET", &cfg.OAuth.Grok.ClientSecret)
	envStr("XAI_OAUTH_AUTHORIZE_URL", &cfg.OAuth.Grok.AuthorizeURL)
	envStr("XAI_OAUTH_TOKEN_URL", &cfg.OAuth.Grok.TokenURL)
	envStr("XAI_OAUTH_SCOPE", &cfg.OAuth.Grok.Scope)
	envStr("XAI_OAUTH_REDIRECT_URI", &cfg.OAuth.Grok.RedirectURL)
	envStr("XAI_OAUTH_REDIRECT_URL", &cfg.OAuth.Grok.RedirectURL)
}

func (c *Config) PostgresDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Host, c.Database.Port, c.Database.User, c.Database.Password, c.Database.DBName, c.Database.SSLMode)
}
