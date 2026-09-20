package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env            string
	HTTPAddr       string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	ShudownTimeout time.Duration

	DatabaseUrl    string
	DBMaxConns     int32
	DBMinConns     int32
	DBConnLifetime time.Duration
	DBPingTimeout  time.Duration

	LogLevel  slog.Level
	LogFormat string

	OTPPepper   []byte
	OTPTTL      time.Duration
	JWTSeed     []byte
	JWTKeyID    string
	AccessTTL   time.Duration
	RefreshTTL  time.Duration
	SMSProvider string

	OTPMaxAttempts int
	OTPWindow      time.Duration
	OTPMaxPerPhone int
	OTPMaxPerIp    int
	SessionMaxAge  time.Duration
	TrustProxy     bool
}

func (c Config) IsProduction() bool {
	return c.Env == "production"
}

type loader struct {
	missing []string
	invalid []string
}

func (l *loader) str(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		if def == "" {
			l.missing = append(l.missing, key)
		}
		return def
	}
	return v
}

func (l *loader) int32(key string, def int32) int32 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n <= 0 {
		l.invalid = append(l.invalid, key)
		return def
	}
	return int32(n)
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		l.invalid = append(l.invalid, key)
		return def
	}
	return d
}

func (l *loader) level(key string, def slog.Level) slog.Level {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		l.invalid = append(l.invalid, key)
		return def
	}
	return lvl
}

func (l *loader) bytesB64(key string, minLen int) []byte {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		l.missing = append(l.missing, key)
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) < minLen {
		l.invalid = append(l.invalid, key)
		return nil
	}
	return b
}

func Load() (Config, error) {
	l := &loader{}

	cfg := Config{
		Env:            l.str("SPACE_ENV", "development"),
		HTTPAddr:       l.str("SPACE_HTTP_ADDR", ":8000"),
		ReadTimeout:    l.duration("SPACE_HTTP_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:   l.duration("SPACE_HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:    l.duration("SPACE_HTTP_IDLE_TIMEOUT", 120*time.Second),
		ShudownTimeout: l.duration("SPACE_SHUTDOWN_TIMEOUT", 20*time.Second),

		DatabaseUrl:    l.str("SPACE_DATABASE_URL", ""),
		DBMaxConns:     l.int32("SPACE_DB_MAX_CONNS", 10),
		DBMinConns:     l.int32("SPACE_DM_MIN_CONNS", 2),
		DBConnLifetime: l.duration("SPACE_DB_CONN_LIFETIME", 55*time.Minute),
		DBPingTimeout:  l.duration("SPACE_DB_PING_TIMEOUT", 2*time.Second),

		LogLevel:  l.level("SPACE_LOG_LEVEL", slog.LevelInfo),
		LogFormat: l.str("SPACE_LOG_FORMAT", "json"),

		OTPPepper:   l.bytesB64("SPACE_OTP_PEPPER", 32),
		OTPTTL:      l.duration("SPACE_OTP_TTL", 5*time.Minute),
		JWTSeed:     l.bytesB64("SPACE_JWT_SEED", 32),
		JWTKeyID:    l.str("SPACE_JWT_KEY_ID", "k1"),
		AccessTTL:   l.duration("SPACE_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:  l.duration("SPACE_REFRESH_TTL", 720*time.Hour),
		SMSProvider: l.str("SPACE_SMS_PROVIDER", "log"),

		OTPMaxAttempts: l.int("SPACE_OTP_MAX_ATTEMPTS", 5),
		OTPWindow:      l.duration("SPACE_OTP_WINDOW", 15*time.Minute),
		OTPMaxPerPhone: l.int("SPACE_OTP_MAX_PER_PHONE", 3),
		OTPMaxPerIp:    l.int("SPACE_OTP_MAX_PER_IP", 15),
		SessionMaxAge:  l.duration("SPACEP_SESSION_MAX_AGE", 2160*time.Hour),
		TrustProxy:     l.boolean("SPACE_TRUST_PROXY", false),
	}

	var problems []string
	if len(l.missing) > 0 {
		problems = append(problems, "missing: "+strings.Join(l.missing, ", "))
	}
	if len(l.invalid) > 0 {
		problems = append(problems, "invalid: "+strings.Join(l.invalid, ", "))
	}
	if cfg.DBMinConns > cfg.DBMaxConns {
		problems = append(problems, "SPACE_DB_MIN_CONNS exceeds SPACE_DB_MAX_CONNS")
	}
	if len(problems) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	if cfg.SMSProvider == "log" && cfg.IsProduction() {
		problems = append(problems, "SPACE_SMS_PROVIDER=log writes OTP codes to the log and is refused in prod")
	}
	return cfg, nil
}

func (l *loader) int(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		l.invalid = append(l.invalid, key)
		return def
	}
	return n
}

func (l *loader) boolean(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.invalid = append(l.invalid, key)
		return def
	}
	return b
}
