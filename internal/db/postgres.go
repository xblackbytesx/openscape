package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a configured pgxpool. Defaults are sensible for a small
// self-hosted deploy; override via env vars if you run more than a couple of
// app replicas:
//
//	DB_MAX_CONNS         (default 10)
//	DB_MIN_CONNS         (default 2)
//	DB_MAX_CONN_LIFETIME (default 1h)
//	DB_MAX_CONN_IDLE     (default 30m)
func NewPool(databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	cfg.MaxConns = int32(envInt("DB_MAX_CONNS", 10))
	cfg.MinConns = int32(envInt("DB_MIN_CONNS", 2))
	cfg.MaxConnLifetime = envDuration("DB_MAX_CONN_LIFETIME", time.Hour)
	cfg.MaxConnIdleTime = envDuration("DB_MAX_CONN_IDLE", 30*time.Minute)
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
