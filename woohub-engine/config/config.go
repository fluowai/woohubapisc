package config

import (
	"os"
	"strings"
)

type Config struct {
	Port               string
	DatabaseURL        string
	APIKey             string
	WebhookURL         string
	MinioEndpoint      string
	MinioBucket        string
	MinioAccessKey     string
	MinioSecretKey     string
	MinioPublicBaseURL string
	MinioUseSSL        bool
	MinioRegion        string
	AutoStartSession   bool
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() *Config {
	endpoint := getEnv("MINIO_ENDPOINT", "https://storage.wootech.com.br")
	// Clean endpoint of http:// or https:// for minio-go client
	cleanEndpoint := strings.TrimPrefix(endpoint, "https://")
	cleanEndpoint = strings.TrimPrefix(cleanEndpoint, "http://")
	cleanEndpoint = strings.TrimRight(cleanEndpoint, "/")

	useSSL := true
	if strings.HasPrefix(endpoint, "http://") || os.Getenv("MINIO_USE_SSL") == "false" {
		useSSL = false
	}

	publicBase := getEnv("MINIO_PUBLIC_BASE_URL", "https://storage.wootech.com.br")
	publicBase = strings.TrimRight(publicBase, "/")

	dbURL := getEnv("DATABASE_URL", "postgresql://postgres.tmcwacoaxkjfmlilmolz:VrtYoeNB5apKoaQV@aws-1-sa-east-1.pooler.supabase.com:5432/postgres")
	// If DATABASE_URL points to pgbouncer transaction pooler on port 6543, use session pooler port 5432 for schema DDL and prepared statements
	if strings.Contains(dbURL, ":6543") {
		dbURL = strings.Replace(dbURL, ":6543", ":5432", 1)
	}

	return &Config{
		Port:               getEnv("PORT", "3001"),
		DatabaseURL:        dbURL,
		APIKey:             getEnv("APIKey", getEnv("API_KEY", "wootech-akg-secret-key-123")),
		WebhookURL:         getEnv("WEBHOOK_URL", "http://woocrm-app:3000/api/whatsapp/webhooks/whatsmeow"),
		MinioEndpoint:      cleanEndpoint,
		MinioBucket:        getEnv("MINIO_BUCKET", "woochat"),
		MinioAccessKey:     getEnv("MINIO_ACCESS_KEY", "VLL4JDNIPJKPPF30PXA4"),
		MinioSecretKey:     getEnv("MINIO_SECRET_KEY", "M7si1sb+Yb9w+g+mVCvNtX+IrTI3EsV+9YPyvfmL"),
		MinioPublicBaseURL: publicBase,
		MinioUseSSL:        useSSL,
		MinioRegion:        getEnv("MINIO_REGION", "us-east-1"),
		AutoStartSession:   true,
	}
}
