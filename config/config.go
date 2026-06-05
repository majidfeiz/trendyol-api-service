package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port            string
	TrendyolBaseURL string
	RequestTimeout  time.Duration // per HTTP request to Trendyol
	MaxConcurrent   int
	CacheTTL        time.Duration
	CacheEnabled    bool
	MaxBatchSize    int
	RetryCount      int           // API retries (keep low)
	TotalTimeout    time.Duration // hard deadline for the entire strategy chain
}

func Load() *Config {
	_ = godotenv.Load()

	return &Config{
		Port:            env("PORT", "8080"),
		TrendyolBaseURL: env("TRENDYOL_BASE_URL", "https://public.trendyol.com"),
		RequestTimeout:  duration("REQUEST_TIMEOUT_SECONDS", 8),  // per HTTP call
		MaxConcurrent:   integer("MAX_CONCURRENT", 20),
		CacheTTL:        duration("CACHE_TTL_SECONDS", 120),
		CacheEnabled:    boolean("CACHE_ENABLED", true),
		MaxBatchSize:    integer("MAX_BATCH_SIZE", 50),
		RetryCount:      integer("RETRY_COUNT", 1),               // 1 retry max
		TotalTimeout:    duration("TOTAL_TIMEOUT_SECONDS", 60),   // max per request
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func integer(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func duration(key string, defSeconds int) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defSeconds) * time.Second
}

func boolean(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}
