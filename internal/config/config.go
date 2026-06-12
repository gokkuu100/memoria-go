package config

import (
	"fmt"
	"os"
	"strconv"
)

type Env string

const (
	EnvDevelopment Env = "development"
	EnvProduction  Env = "production"
)

type Config struct {
	Env         Env
	Port        string
	DatabaseURL string
	JWTSecret   string

	// SMTP (Gmail at launch). If SMTPHost is empty in development, emails are
	// logged instead of sent.
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// S3-compatible media storage (MinIO locally, R2 in production).
	// S3PublicEndpoint is the host clients reach in presigned URLs — inside
	// compose the API talks to http://minio:9000 but phones/curl need
	// http://localhost:9000 (or the CDN host in production).
	S3Endpoint       string
	S3PublicEndpoint string
	S3AccessKey      string
	S3SecretKey      string
	S3Bucket         string
	S3UsePathStyle   bool

	// Expo Push (Phase B4+).
	ExpoPushURL     string
	ExpoAccessToken string

	// RevenueCat subscriptions (Phase B8).
	RevenueCatWebhookSecret string
	RevenueCatProductPlus   string
	RevenueCatProductPro    string

	// Optional observability (B11).
	SentryDSN string

	// Postgres pool sizing (default 20).
	DBMaxConns int32
}

// Load reads configuration from environment variables. DATABASE_URL is
// required; everything else has a development default.
func Load() (*Config, error) {
	cfg := &Config{
		Env:          Env(getenv("ENV", string(EnvDevelopment))),
		Port:         getenv("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		JWTSecret:    os.Getenv("JWT_SECRET"),
		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     getenv("SMTP_PORT", "587"),
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     os.Getenv("SMTP_FROM"),

		S3Endpoint:       getenv("S3_ENDPOINT", "http://localhost:9000"),
		S3PublicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
		S3AccessKey:      getenv("S3_ACCESS_KEY", "memoria"),
		S3SecretKey:      getenv("S3_SECRET_KEY", "memoria-secret"),
		S3Bucket:         getenv("S3_BUCKET", "memoria-media"),
		S3UsePathStyle:   getenv("S3_USE_PATH_STYLE", "true") == "true",

		ExpoPushURL:     getenv("EXPO_PUSH_URL", "https://exp.host/--/api/v2/push/send"),
		ExpoAccessToken: os.Getenv("EXPO_ACCESS_TOKEN"),

		RevenueCatWebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
		RevenueCatProductPlus:   getenv("REVENUECAT_PRODUCT_PLUS", "memoria_plus"),
		RevenueCatProductPro:    getenv("REVENUECAT_PRODUCT_PRO", "memoria_pro"),

		SentryDSN:  os.Getenv("SENTRY_DSN"),
		DBMaxConns: int32(getenvInt("DB_MAX_CONNS", 20)),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.Env != EnvDevelopment && cfg.Env != EnvProduction {
		return nil, fmt.Errorf("ENV must be %q or %q, got %q", EnvDevelopment, EnvProduction, cfg.Env)
	}
	if cfg.JWTSecret == "" {
		if cfg.Env == EnvProduction {
			return nil, fmt.Errorf("JWT_SECRET is required in production")
		}
		cfg.JWTSecret = "dev-only-secret-do-not-use-in-prod"
	}
	if cfg.Env == EnvProduction && cfg.SMTPHost == "" {
		return nil, fmt.Errorf("SMTP_HOST is required in production")
	}
	if cfg.SMTPFrom == "" {
		cfg.SMTPFrom = cfg.SMTPUsername
	}
	if cfg.Env == EnvProduction {
		for name, v := range map[string]string{
			"S3_ENDPOINT":   os.Getenv("S3_ENDPOINT"),
			"S3_ACCESS_KEY": os.Getenv("S3_ACCESS_KEY"),
			"S3_SECRET_KEY": os.Getenv("S3_SECRET_KEY"),
			"S3_BUCKET":     os.Getenv("S3_BUCKET"),
		} {
			if v == "" {
				return nil, fmt.Errorf("%s is required in production", name)
			}
		}
	}
	if cfg.S3PublicEndpoint == "" {
		cfg.S3PublicEndpoint = cfg.S3Endpoint
	}
	if cfg.RevenueCatWebhookSecret == "" {
		if cfg.Env == EnvProduction {
			return nil, fmt.Errorf("REVENUECAT_WEBHOOK_SECRET is required in production")
		}
		cfg.RevenueCatWebhookSecret = "dev-revenuecat-webhook-secret"
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}
