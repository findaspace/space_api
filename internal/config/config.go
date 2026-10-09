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

	OTPMaxAttempts  int
	OTPWindow       time.Duration
	OTPMaxPerPhone  int
	OTPMaxPerIp     int
	SessionMaxAge   time.Duration
	TrustProxy      bool
	DirectContact   bool
	ContactDailyCap int
	PaymentsEnabled bool

	StorageEndpoint      string
	StorageRegion        string
	StorageBucket        string
	StorageAccessKey     string
	StorageSecretKey     string
	StoragePathStyle     bool
	StoragePublicBaseURL string
	UploadURLTTL         time.Duration
	MediaMaxBytes        int64
	MediaMaxPerSpace     int

	BookingHoldTTL time.Duration

	PaystackSecret      string
	PaystackBaseURL     string
	PaystackCallbackURL string
	PaystackTimeout     time.Duration

	PayoutHoldDays int

	ArkeselAPIKey  string
	SMSSenderID    string
	ArkeselSandbox bool

	RateLimitEnabled bool
	RateLimitAuth    int
	RateLimitRead    int
	RateLimitWrite   int
	RateLimitWebhook int
	CORSOrigins      []string
	RequestTimeout   time.Duration
	DBExecMode       string
	DBStatementCache int

	MetricsAddr       string
	WorkerConcurrency int

	FeeNightlyBps    int
	FeeTermBps       int
	FeeTermCapMonths int
	FeeMinMinor      int64

	OSRMBaseURL     string
	OSRMProfile     string
	OSRMTimeout     time.Duration
	GeoRadiusM      int
	GeoMaxLandmarks int

	VideoMaxBytes    int64
	FFmpegPath       string
	FFprobePath      string
	TranscodeTimeout time.Duration
	TranscodeWorkDir string
	VideoMaxSeconds  int

	RefreshGrace      time.Duration
	BFFSecret         string
	GoogleClientID    string
	MicrosoftClientID string
	AppleClientID     string

	BookingRequestTTL time.Duration
	BookingPaymentTTL time.Duration
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

func (l *loader) int64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		l.invalid = append(l.invalid, key)
		return def
	}
	return n
}

func Load() (Config, error) {
	l := &loader{}

	cfg := Config{
		GoogleClientID:    strings.TrimSpace(os.Getenv("SPACE_GOOGLE_CLIENT_ID")),
		MicrosoftClientID: strings.TrimSpace(os.Getenv("SPACE_MICROSOFT_CLIENT_ID")),
		AppleClientID:     strings.TrimSpace(os.Getenv("SPACE_APPLE_CLIENT_ID")),
		Env:               l.str("SPACE_ENV", "development"),
		HTTPAddr:          l.str("SPACE_HTTP_ADDR", ":8000"),
		ReadTimeout:       l.duration("SPACE_HTTP_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      l.duration("SPACE_HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       l.duration("SPACE_HTTP_IDLE_TIMEOUT", 120*time.Second),
		ShudownTimeout:    l.duration("SPACE_SHUTDOWN_TIMEOUT", 20*time.Second),

		DatabaseUrl:    l.str("SPACE_DATABASE_URL", ""),
		DBMaxConns:     l.int32("SPACE_DB_MAX_CONNS", 10),
		DBMinConns:     l.int32("SPACE_DB_MIN_CONNS", 2),
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

		OTPMaxAttempts:  l.int("SPACE_OTP_MAX_ATTEMPTS", 5),
		OTPWindow:       l.duration("SPACE_OTP_WINDOW", 15*time.Minute),
		OTPMaxPerPhone:  l.int("SPACE_OTP_MAX_PER_PHONE", 3),
		OTPMaxPerIp:     l.int("SPACE_OTP_MAX_PER_IP", 15),
		SessionMaxAge:   l.duration("SPACEP_SESSION_MAX_AGE", 2160*time.Hour),
		TrustProxy:      l.boolean("SPACE_TRUST_PROXY", false),
		DirectContact:   l.boolean("SPACE_DIRECT_CONTACT", true),
		ContactDailyCap: l.int("SPACE_CONTACT_DAILY_CAP", 20),
		PaymentsEnabled: l.boolean("SPACE_PAYMENTS_ENABLED", false),

		StorageEndpoint:      l.str("SPACE_STORAGE_ENDPOINT", ""),
		StorageRegion:        l.str("SPACE_STORAGE_REGION", "auto"),
		StorageBucket:        l.str("SPACE_STORAGE_BUCKET", ""),
		StorageAccessKey:     l.str("SPACE_STORAGE_ACCESS_KEY", ""),
		StorageSecretKey:     l.str("SPACE_STORAGE_SECRET_KEY", ""),
		StoragePathStyle:     l.boolean("SPACE_STORAGE_PATH_STYLE", true),
		StoragePublicBaseURL: l.str("SPACE_STORAGE_PUBLIC_BASE_URL", ""),
		UploadURLTTL:         l.duration("SPACE_UPLOAD_URL_TTL", 15*time.Minute),
		MediaMaxBytes:        l.int64("SPACE_MEDIA_MAX_BYTES", 10<<20),
		MediaMaxPerSpace:     l.int("SPACE_MEDIA_MAX_PER_SPACE", 20),

		// PlatformFeeBps: l.int("SPACE_PLATFORM_FEE_BPS", 1000),

		BookingHoldTTL: l.duration("SPACE_BOOKING_HOLD_TTL", 30*time.Minute),

		PaystackSecret:      strings.TrimSpace(os.Getenv("SPACE_PAYSTACK_SECRET")),
		PaystackBaseURL:     l.str("SPACE_PAYSTACK_BASE_URL", "https://api.paystack.co"),
		PaystackCallbackURL: strings.TrimSpace(os.Getenv("SPACE_PAYSTACK_CALLBACK_URL")),
		PaystackTimeout:     l.duration("SPACE_PAYSTACK_TIMEOUT", 20*time.Second),
		PayoutHoldDays:      l.int("SPACE_PAYOUT_HOLD_DAYS", 2),

		ArkeselAPIKey:  l.str("SPACE_ARKESEL_API_KEY", "unset"),
		SMSSenderID:    l.str("SPACE_SMS_SENDER_ID", "Space"),
		ArkeselSandbox: l.boolean("SPACE_ARKESEL_SANDBOX", true),

		RateLimitEnabled: l.boolean("SPACE_RATELIMIT_ENABLED", true),
		RateLimitAuth:    l.int("SPACE_RATELIMIT_AUTH_PER_MIN", 10),
		RateLimitRead:    l.int("SPACE_RATELIMIT_READ_PER_MIN", 240),
		RateLimitWrite:   l.int("SPACE_RATELIMIT_WRITE_PER_MIN", 60),
		RateLimitWebhook: l.int("SPACE_RATELIMIT_WEBHOOK_PER_MIN", 600),
		CORSOrigins:      l.list("SPACE_CORS_ORIGINS", nil),
		RequestTimeout:   l.duration("SPACE_REQUEST_TIMEOUT", 15*time.Second),
		DBExecMode:       l.str("SPACE_DB_EXEC_MODE", "cache_statement"),
		DBStatementCache: l.int("SPACE_DB_STATEMENT_CACHE", 512),

		MetricsAddr:       l.str("SPACE_METRICS_ADDR", ":9091"),
		WorkerConcurrency: l.int("SPACE_WORKER_CONCURRENCY", 10),

		FeeNightlyBps:    l.int("SPACE_FEE_NIGHTLY_BPS", 1000),
		FeeTermBps:       l.int("SPACE_FEE_TERM_BPS", 1000),
		FeeTermCapMonths: l.int("SPACE_FEE_TERM_CAP_MONTHS", 1),
		FeeMinMinor:      l.int64("SPACE_FEE_MIN_MINOR", 2000),

		OSRMBaseURL:     l.str("SPACE_OSRM_URL", "http://localhost:5000"),
		OSRMProfile:     l.str("SPACE_OSRM_PROFILE", "foot"),
		OSRMTimeout:     l.duration("SPACE_OSRM_TIMEOUT", 10*time.Second),
		GeoRadiusM:      l.int("SPACE_GEO_RADIUS_M", 5000),
		GeoMaxLandmarks: l.int("SPACE_GEO_MAX_LANDMARKS", 12),

		VideoMaxBytes:    l.int64("SPACE_VIDEO_MAX_BYTES", 100<<20),
		FFmpegPath:       l.str("SPACE_FFMPEG_PATH", "ffmpeg"),
		FFprobePath:      l.str("SPACE_FFPROBE_PATH", "ffprobe"),
		TranscodeTimeout: l.duration("SPACE_TRANSCODE_TIMEOUT", 5*time.Minute),
		TranscodeWorkDir: l.str("SPACE_TRANSCODE_WORKDIR", os.TempDir()),
		VideoMaxSeconds:  l.int("SPACE_VIDEO_MAX_SECONDS", 60),

		RefreshGrace: l.duration("SPACE_REFRESH_GRACE", 30*time.Second),
		BFFSecret:    l.str("SPACE_BFF_SECRET", ""),

		BookingRequestTTL: l.duration("SPACE_BOOKING_REQUEST_TTL", 48*time.Hour),
		BookingPaymentTTL: l.duration("SPACE_BOOKING_PAYMENT_TTL", 24*time.Hour),
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
	if cfg.SMSProvider != "log" && cfg.SMSProvider != "arkesel" {
		problems = append(problems, "SPACE_SMS_PROVIDER must be log or arkesel")
	}
	if cfg.SMSProvider == "log" && cfg.IsProduction() {
		problems = append(problems, "SPACE_SMS_PROVIDER=log writes OTP codes to the log and is refused in production")
	}
	if cfg.SMSProvider == "arkesel" {
		if cfg.ArkeselAPIKey == "unset" || len(cfg.ArkeselAPIKey) < 8 {
			problems = append(problems, "SPACE_ARKESEL_API_KEY is required for arkesel")
		}
		if len(cfg.SMSSenderID) > 11 {
			problems = append(problems, "SPACE_SMS_SENDER_ID must be at most 11 characters")
		}
	}
	if cfg.IsProduction() && cfg.PaymentsEnabled && !strings.HasPrefix(cfg.PaystackSecret, "sk_live_") {
		problems = append(problems, "SPACE_PAYSTACK_SECRET must be a live key in production")
	}
	if cfg.IsProduction() && cfg.PaymentsEnabled && cfg.PaystackBaseURL != "https://api.paystack.co" {
		problems = append(problems, "SPACE_PAYSTACK_BASE_URL must be Paystack in production")
	}
	if len(problems) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	if cfg.IsProduction() && cfg.SMSProvider == "arkesel" && cfg.ArkeselSandbox {
		problems = append(problems, "SPACE_ARKESEL_SANDBOX must be false in production")
	}
	if cfg.IsProduction() && !cfg.RateLimitEnabled {
		problems = append(problems, "SPACE_RATELIMIT_ENABLED must not be false in production")
	}
	if cfg.IsProduction() && len(cfg.CORSOrigins) == 0 {
		problems = append(problems, "SPACE_CORS_ORIGINS must list the web origins in production")
	}
	if cfg.IsProduction() && len(cfg.BFFSecret) < 32 {
		problems = append(problems, "SPACE_BFF_SECRET must be at least 32 characters in production")
	}
	if cfg.UploadURLTTL > time.Hour {
		problems = append(problems, "SPACE_UPLOAD_URL_TTL must not exceed one hour; account media cleanup depends on upload links expiring")
	}
	if cfg.RefreshGrace > 2*time.Minute {
		problems = append(problems, "SPACE_REFRESH_GRACE above two minutes weakens theft detection on refresh tokens")
	}
	if cfg.BookingPaymentTTL < time.Hour || cfg.BookingRequestTTL < time.Hour {
		problems = append(problems, "SPACE_BOOKING_REQUEST_TTL and SPACE_BOOKING_PAYMENT_TTL must be at least an hour")
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(problems, "; "))
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

func (l *loader) list(key string, def []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
