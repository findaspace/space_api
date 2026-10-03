package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func configEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{"SPACE_ENV": "production", "SPACE_DATABASE_URL": "postgres://test", "SPACE_OTP_PEPPER": base64.StdEncoding.EncodeToString(make([]byte, 32)), "SPACE_JWT_SEED": base64.StdEncoding.EncodeToString(make([]byte, 32)), "SPACE_STORAGE_ENDPOINT": "https://storage.example", "SPACE_STORAGE_BUCKET": "test", "SPACE_STORAGE_ACCESS_KEY": "test", "SPACE_STORAGE_SECRET_KEY": "test", "SPACE_STORAGE_PUBLIC_BASE_URL": "https://media.example", "SPACE_BFF_SECRET": strings.Repeat("a", 32), "SPACE_SMS_PROVIDER": "arkesel", "SPACE_ARKESEL_SANDBOX": "false", "SPACE_ARKESEL_API_KEY": "test-key-placeholder", "SPACE_RATELIMIT_ENABLED": "true", "SPACE_CORS_ORIGINS": "https://space.example", "SPACE_PAYMENTS_ENABLED": "false", "SPACE_PAYSTACK_SECRET": "", "SPACE_PAYSTACK_CALLBACK_URL": ""} {
		t.Setenv(k, v)
	}
}
func TestDirectContactDoesNotRequirePaystack(t *testing.T) {
	configEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PaymentsEnabled || !cfg.DirectContact {
		t.Fatal("incorrect default product policy")
	}
}
func TestLateProductionErrorsAreReturned(t *testing.T) {
	configEnv(t)
	t.Setenv("SPACE_RATELIMIT_ENABLED", "false")
	if _, err := Load(); err == nil {
		t.Fatal("invalid production config accepted")
	}
}

func TestArkeselRequiresKey(t *testing.T) {
	configEnv(t)
	t.Setenv("SPACE_ARKESEL_API_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("missing live SMS key accepted")
	}
}
func TestDBMinConnsUsesDocumentedName(t *testing.T) {
	configEnv(t)
	t.Setenv("SPACE_DB_MIN_CONNS", "3")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBMinConns != 3 {
		t.Fatal("pool minimum ignored")
	}
}

func TestWorkerConcurrencyForSmallBeta(t *testing.T) {
	configEnv(t)
	t.Setenv("SPACE_WORKER_CONCURRENCY", "1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerConcurrency != 1 {
		t.Fatal("worker concurrency ignored")
	}
	t.Setenv("SPACE_WORKER_CONCURRENCY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero workers accepted")
	}
}
