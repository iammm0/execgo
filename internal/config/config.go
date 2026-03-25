// Package config 管理 ExecGo 的配置 / manages ExecGo configuration.
package config

import (
	"flag"
	"os"
	"strconv"
)

// Config 全局配置 / global configuration.
type Config struct {
	HTTPAddr       string // 监听地址 / listen address
	DataDir        string // 数据持久化目录 / data persistence directory
	MaxConcurrency int    // 最大并发数 / max concurrency
	ShutdownTimeout int   // 优雅关闭超时(秒) / graceful shutdown timeout in seconds
}

// Load 从 flag 和环境变量加载配置 / loads config from flags and env vars.
// 优先级: flag > env > default / priority: flag > env > default
func Load() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.HTTPAddr, "addr", envOrDefault("EXECGO_ADDR", ":8080"), "HTTP listen address")
	flag.StringVar(&cfg.DataDir, "data-dir", envOrDefault("EXECGO_DATA_DIR", "data"), "data directory for persistence")
	flag.IntVar(&cfg.MaxConcurrency, "max-concurrency", envOrDefaultInt("EXECGO_MAX_CONCURRENCY", 10), "max concurrent task executions")
	flag.IntVar(&cfg.ShutdownTimeout, "shutdown-timeout", envOrDefaultInt("EXECGO_SHUTDOWN_TIMEOUT", 15), "graceful shutdown timeout in seconds")

	flag.Parse()
	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
