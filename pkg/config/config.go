// Package config manages ExecGo configuration.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	KeyHTTPAddr        = "http_addr"
	KeyGRPCAddr        = "grpc_addr"
	KeyDataDir         = "data_dir"
	KeyMaxConcurrency  = "max_concurrency"
	KeyShutdownTimeout = "shutdown_timeout"

	KeyEventStoreBackend     = "event_store_backend"
	KeyEventStoreSQLitePath  = "event_store_sqlite_path"
	KeyEventStorePostgresDSN = "event_store_postgres_dsn"

	KeyQueueBackend  = "queue_backend"
	KeyRedisAddr     = "redis_addr"
	KeyRedisPassword = "redis_password"
	KeyRedisDB       = "redis_db"
	KeyRedisPrefix   = "redis_prefix"
	KeyRedisGroup    = "redis_group"

	KeyWorkerID           = "worker_id"
	KeyWorkerConcurrency  = "worker_concurrency"
	KeyHeartbeatSeconds   = "heartbeat_seconds"
	KeyLeaseSeconds       = "lease_seconds"
	KeyLeaseSweepSeconds  = "lease_sweep_seconds"
	KeyWorkerStaleSeconds = "worker_stale_seconds"

	KeyRedisClaimMinIdleSeconds = "redis_claim_min_idle_seconds"
	KeyRedisClaimBatch          = "redis_claim_batch"

	KeySandboxMode     = "sandbox_mode"
	KeyDockerImage     = "docker_image"
	KeyDockerCPUs      = "docker_cpus"
	KeyDockerMemory    = "docker_memory"
	KeyDockerPidsLimit = "docker_pids_limit"
	KeyDockerNoNetwork = "docker_no_network"
)

const (
	EnvHTTPAddr        = "EXECGO_ADDR"
	EnvGRPCAddr        = "EXECGO_GRPC_ADDR"
	EnvDataDir         = "EXECGO_DATA_DIR"
	EnvMaxConcurrency  = "EXECGO_MAX_CONCURRENCY"
	EnvShutdownTimeout = "EXECGO_SHUTDOWN_TIMEOUT"

	EnvEventStoreBackend     = "EXECGO_EVENT_STORE_BACKEND"
	EnvEventStoreSQLitePath  = "EXECGO_EVENT_STORE_SQLITE_PATH"
	EnvEventStorePostgresDSN = "EXECGO_EVENT_STORE_POSTGRES_DSN"

	EnvQueueBackend  = "EXECGO_QUEUE_BACKEND"
	EnvRedisAddr     = "EXECGO_REDIS_ADDR"
	EnvRedisPassword = "EXECGO_REDIS_PASSWORD"
	EnvRedisDB       = "EXECGO_REDIS_DB"
	EnvRedisPrefix   = "EXECGO_REDIS_PREFIX"
	EnvRedisGroup    = "EXECGO_REDIS_GROUP"

	EnvWorkerID           = "EXECGO_WORKER_ID"
	EnvWorkerConcurrency  = "EXECGO_WORKER_CONCURRENCY"
	EnvHeartbeatSeconds   = "EXECGO_HEARTBEAT_SECONDS"
	EnvLeaseSeconds       = "EXECGO_LEASE_SECONDS"
	EnvLeaseSweepSeconds  = "EXECGO_LEASE_SWEEP_INTERVAL_SECONDS"
	EnvWorkerStaleSeconds = "EXECGO_WORKER_STALE_SECONDS"

	EnvRedisClaimMinIdleSeconds = "EXECGO_REDIS_CLAIM_MIN_IDLE_SECONDS"
	EnvRedisClaimBatch          = "EXECGO_REDIS_CLAIM_BATCH"

	EnvSandboxMode     = "EXECGO_SANDBOX_MODE"
	EnvDockerImage     = "EXECGO_DOCKER_IMAGE"
	EnvDockerCPUs      = "EXECGO_DOCKER_CPUS"
	EnvDockerMemory    = "EXECGO_DOCKER_MEMORY"
	EnvDockerPidsLimit = "EXECGO_DOCKER_PIDS_LIMIT"
	EnvDockerNoNetwork = "EXECGO_DOCKER_NO_NETWORK"
)

// Config defines runtime configuration.
type Config struct {
	HTTPAddr        string
	GRPCAddr        string
	DataDir         string
	MaxConcurrency  int
	ShutdownTimeout int

	EventStoreBackend     string
	EventStoreSQLitePath  string
	EventStorePostgresDSN string

	QueueBackend  string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisPrefix   string
	RedisGroup    string

	WorkerID           string
	WorkerConcurrency  int
	HeartbeatSeconds   int
	LeaseSeconds       int
	LeaseSweepSeconds  int
	WorkerStaleSeconds int

	RedisClaimMinIdleSeconds int
	RedisClaimBatch          int

	SandboxMode     string
	DockerImage     string
	DockerCPUs      string
	DockerMemory    string
	DockerPidsLimit int
	DockerNoNetwork bool
}

// Provider abstracts config sources.
type Provider interface {
	GetString(key string, defaultVal string) string
	GetInt(key string, defaultVal int) int
	GetBool(key string, defaultVal bool) bool
}

// Load builds Config from Provider.
func Load(p Provider) *Config {
	cfg := &Config{
		HTTPAddr:        p.GetString(KeyHTTPAddr, ":8080"),
		GRPCAddr:        p.GetString(KeyGRPCAddr, ":50051"),
		DataDir:         p.GetString(KeyDataDir, "data"),
		MaxConcurrency:  p.GetInt(KeyMaxConcurrency, 10),
		ShutdownTimeout: p.GetInt(KeyShutdownTimeout, 15),

		EventStoreBackend:     normalizeChoice(p.GetString(KeyEventStoreBackend, "memory")),
		EventStoreSQLitePath:  p.GetString(KeyEventStoreSQLitePath, ""),
		EventStorePostgresDSN: p.GetString(KeyEventStorePostgresDSN, ""),

		QueueBackend:  normalizeChoice(p.GetString(KeyQueueBackend, "memory")),
		RedisAddr:     p.GetString(KeyRedisAddr, ""),
		RedisPassword: p.GetString(KeyRedisPassword, ""),
		RedisDB:       p.GetInt(KeyRedisDB, 0),
		RedisPrefix:   p.GetString(KeyRedisPrefix, "execgo"),
		RedisGroup:    p.GetString(KeyRedisGroup, "execgo-workers"),

		WorkerID:           p.GetString(KeyWorkerID, "worker-local"),
		WorkerConcurrency:  p.GetInt(KeyWorkerConcurrency, 4),
		HeartbeatSeconds:   p.GetInt(KeyHeartbeatSeconds, 5),
		LeaseSeconds:       p.GetInt(KeyLeaseSeconds, 30),
		LeaseSweepSeconds:  p.GetInt(KeyLeaseSweepSeconds, 5),
		WorkerStaleSeconds: p.GetInt(KeyWorkerStaleSeconds, 0),

		RedisClaimMinIdleSeconds: p.GetInt(KeyRedisClaimMinIdleSeconds, 0),
		RedisClaimBatch:          p.GetInt(KeyRedisClaimBatch, 10),

		SandboxMode:     normalizeChoice(p.GetString(KeySandboxMode, "local")),
		DockerImage:     p.GetString(KeyDockerImage, "alpine:3.21"),
		DockerCPUs:      p.GetString(KeyDockerCPUs, ""),
		DockerMemory:    p.GetString(KeyDockerMemory, ""),
		DockerPidsLimit: p.GetInt(KeyDockerPidsLimit, 128),
		DockerNoNetwork: p.GetBool(KeyDockerNoNetwork, false),
	}
	if cfg.EventStoreSQLitePath == "" {
		cfg.EventStoreSQLitePath = cfg.DataDir + "/eventlog.sqlite"
	}
	if cfg.WorkerStaleSeconds <= 0 {
		cfg.WorkerStaleSeconds = cfg.HeartbeatSeconds * 3
	}
	if cfg.RedisClaimMinIdleSeconds <= 0 {
		cfg.RedisClaimMinIdleSeconds = cfg.LeaseSeconds
	}
	if cfg.RedisClaimBatch <= 0 {
		cfg.RedisClaimBatch = 10
	}
	return cfg
}

// Validate checks whether the loaded configuration is executable.
func (cfg *Config) Validate() error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	switch cfg.EventStoreBackend {
	case "memory":
	case "sqlite":
		if strings.TrimSpace(cfg.EventStoreSQLitePath) == "" {
			return fmt.Errorf("sqlite event store requires %s", EnvEventStoreSQLitePath)
		}
	case "postgres":
		if strings.TrimSpace(cfg.EventStorePostgresDSN) == "" {
			return fmt.Errorf("postgres event store requires %s", EnvEventStorePostgresDSN)
		}
	default:
		return fmt.Errorf("unsupported event store backend %q (supported: memory, sqlite, postgres)", cfg.EventStoreBackend)
	}

	switch cfg.QueueBackend {
	case "memory":
	case "redis":
		if strings.TrimSpace(cfg.RedisAddr) == "" {
			return fmt.Errorf("redis queue requires %s", EnvRedisAddr)
		}
	default:
		return fmt.Errorf("unsupported queue backend %q (supported: memory, redis)", cfg.QueueBackend)
	}

	switch cfg.SandboxMode {
	case "local", "docker":
	default:
		return fmt.Errorf("unsupported sandbox mode %q (supported: local, docker)", cfg.SandboxMode)
	}

	if cfg.MaxConcurrency <= 0 {
		return fmt.Errorf("max concurrency must be positive")
	}
	if cfg.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout must be positive")
	}
	if cfg.RedisDB < 0 {
		return fmt.Errorf("redis db must be non-negative")
	}
	if cfg.WorkerConcurrency <= 0 {
		return fmt.Errorf("worker concurrency must be positive")
	}
	if cfg.HeartbeatSeconds <= 0 {
		return fmt.Errorf("heartbeat seconds must be positive")
	}
	if cfg.LeaseSeconds <= 0 {
		return fmt.Errorf("lease seconds must be positive")
	}
	if cfg.LeaseSweepSeconds <= 0 {
		return fmt.Errorf("lease sweep seconds must be positive")
	}
	if cfg.WorkerStaleSeconds <= 0 {
		return fmt.Errorf("worker stale seconds must be positive")
	}
	if cfg.RedisClaimMinIdleSeconds <= 0 {
		return fmt.Errorf("redis claim min idle seconds must be positive")
	}
	if cfg.RedisClaimBatch <= 0 {
		return fmt.Errorf("redis claim batch must be positive")
	}
	if cfg.DockerPidsLimit <= 0 {
		return fmt.Errorf("docker pids limit must be positive")
	}
	return nil
}

// FlagEnvProvider uses CLI flags with env defaults.
type FlagEnvProvider struct {
	httpAddr        string
	grpcAddr        string
	dataDir         string
	maxConcurrency  int
	shutdownTimeout int

	eventStoreBackend     string
	eventStoreSQLitePath  string
	eventStorePostgresDSN string

	queueBackend  string
	redisAddr     string
	redisPassword string
	redisDB       int
	redisPrefix   string
	redisGroup    string

	workerID           string
	workerConcurrency  int
	heartbeatSeconds   int
	leaseSeconds       int
	leaseSweepSeconds  int
	workerStaleSeconds int

	redisClaimMinIdleSeconds int
	redisClaimBatch          int

	sandboxMode     string
	dockerImage     string
	dockerCPUs      string
	dockerMemory    string
	dockerPidsLimit int
	dockerNoNetwork bool
}

// NewFlagEnvProvider registers and parses flags.
func NewFlagEnvProvider() *FlagEnvProvider {
	p := &FlagEnvProvider{}
	flag.StringVar(&p.httpAddr, "addr", envOrDefault(EnvHTTPAddr, ":8080"), "HTTP listen address")
	flag.StringVar(&p.grpcAddr, "grpc-addr", envOrDefault(EnvGRPCAddr, ":50051"), "gRPC listen address")
	flag.StringVar(&p.dataDir, "data-dir", envOrDefault(EnvDataDir, "data"), "data directory")
	flag.IntVar(&p.maxConcurrency, "max-concurrency", envOrDefaultInt(EnvMaxConcurrency, 10), "max concurrent executions")
	flag.IntVar(&p.shutdownTimeout, "shutdown-timeout", envOrDefaultInt(EnvShutdownTimeout, 15), "graceful shutdown timeout in seconds")

	flag.StringVar(&p.eventStoreBackend, "event-store", envOrDefault(EnvEventStoreBackend, "memory"), "event store backend: memory|sqlite|postgres")
	flag.StringVar(&p.eventStoreSQLitePath, "event-store-sqlite", envOrDefault(EnvEventStoreSQLitePath, ""), "sqlite event store path")
	flag.StringVar(&p.eventStorePostgresDSN, "event-store-postgres-dsn", envOrDefault(EnvEventStorePostgresDSN, ""), "postgres event store DSN")

	flag.StringVar(&p.queueBackend, "queue-backend", envOrDefault(EnvQueueBackend, "memory"), "queue backend: memory|redis")
	flag.StringVar(&p.redisAddr, "redis-addr", envOrDefault(EnvRedisAddr, ""), "redis address")
	flag.StringVar(&p.redisPassword, "redis-password", envOrDefault(EnvRedisPassword, ""), "redis password")
	flag.IntVar(&p.redisDB, "redis-db", envOrDefaultInt(EnvRedisDB, 0), "redis db")
	flag.StringVar(&p.redisPrefix, "redis-prefix", envOrDefault(EnvRedisPrefix, "execgo"), "redis stream key prefix")
	flag.StringVar(&p.redisGroup, "redis-group", envOrDefault(EnvRedisGroup, "execgo-workers"), "redis consumer group")

	flag.StringVar(&p.workerID, "worker-id", envOrDefault(EnvWorkerID, "worker-local"), "worker id")
	flag.IntVar(&p.workerConcurrency, "worker-concurrency", envOrDefaultInt(EnvWorkerConcurrency, 4), "worker concurrency")
	flag.IntVar(&p.heartbeatSeconds, "heartbeat-seconds", envOrDefaultInt(EnvHeartbeatSeconds, 5), "worker heartbeat interval")
	flag.IntVar(&p.leaseSeconds, "lease-seconds", envOrDefaultInt(EnvLeaseSeconds, 30), "task lease duration")
	flag.IntVar(&p.leaseSweepSeconds, "lease-sweep-interval", envOrDefaultInt(EnvLeaseSweepSeconds, 5), "lease recovery sweep interval in seconds")
	flag.IntVar(&p.workerStaleSeconds, "worker-stale-seconds", envOrDefaultInt(EnvWorkerStaleSeconds, 0), "worker stale threshold in seconds; defaults to heartbeat interval * 3")
	flag.IntVar(&p.redisClaimMinIdleSeconds, "redis-claim-min-idle", envOrDefaultInt(EnvRedisClaimMinIdleSeconds, 0), "redis pending message reclaim idle threshold in seconds; defaults to lease duration")
	flag.IntVar(&p.redisClaimBatch, "redis-claim-batch", envOrDefaultInt(EnvRedisClaimBatch, 10), "redis pending message reclaim batch size")

	flag.StringVar(&p.sandboxMode, "sandbox", envOrDefault(EnvSandboxMode, "local"), "sandbox mode: local|docker")
	flag.StringVar(&p.dockerImage, "docker-image", envOrDefault(EnvDockerImage, "alpine:3.21"), "docker sandbox image")
	flag.StringVar(&p.dockerCPUs, "docker-cpus", envOrDefault(EnvDockerCPUs, ""), "docker CPU quota")
	flag.StringVar(&p.dockerMemory, "docker-memory", envOrDefault(EnvDockerMemory, ""), "docker memory limit")
	flag.IntVar(&p.dockerPidsLimit, "docker-pids-limit", envOrDefaultInt(EnvDockerPidsLimit, 128), "docker pids limit")
	flag.BoolVar(&p.dockerNoNetwork, "docker-no-network", envOrDefaultBool(EnvDockerNoNetwork, false), "disable network in docker sandbox")

	flag.Parse()
	return p
}

func (p *FlagEnvProvider) GetString(key string, defaultVal string) string {
	switch key {
	case KeyHTTPAddr:
		return nonEmpty(p.httpAddr, defaultVal)
	case KeyGRPCAddr:
		return nonEmpty(p.grpcAddr, defaultVal)
	case KeyDataDir:
		return nonEmpty(p.dataDir, defaultVal)
	case KeyEventStoreBackend:
		return nonEmpty(p.eventStoreBackend, defaultVal)
	case KeyEventStoreSQLitePath:
		return nonEmpty(p.eventStoreSQLitePath, defaultVal)
	case KeyEventStorePostgresDSN:
		return nonEmpty(p.eventStorePostgresDSN, defaultVal)
	case KeyQueueBackend:
		return nonEmpty(p.queueBackend, defaultVal)
	case KeyRedisAddr:
		return nonEmpty(p.redisAddr, defaultVal)
	case KeyRedisPassword:
		return p.redisPassword
	case KeyRedisPrefix:
		return nonEmpty(p.redisPrefix, defaultVal)
	case KeyRedisGroup:
		return nonEmpty(p.redisGroup, defaultVal)
	case KeyWorkerID:
		return nonEmpty(p.workerID, defaultVal)
	case KeySandboxMode:
		return nonEmpty(p.sandboxMode, defaultVal)
	case KeyDockerImage:
		return nonEmpty(p.dockerImage, defaultVal)
	case KeyDockerCPUs:
		return nonEmpty(p.dockerCPUs, defaultVal)
	case KeyDockerMemory:
		return nonEmpty(p.dockerMemory, defaultVal)
	default:
		return defaultVal
	}
}

func (p *FlagEnvProvider) GetInt(key string, defaultVal int) int {
	switch key {
	case KeyMaxConcurrency:
		if p.maxConcurrency > 0 {
			return p.maxConcurrency
		}
	case KeyShutdownTimeout:
		if p.shutdownTimeout > 0 {
			return p.shutdownTimeout
		}
	case KeyRedisDB:
		return p.redisDB
	case KeyWorkerConcurrency:
		if p.workerConcurrency > 0 {
			return p.workerConcurrency
		}
	case KeyHeartbeatSeconds:
		if p.heartbeatSeconds > 0 {
			return p.heartbeatSeconds
		}
	case KeyLeaseSeconds:
		if p.leaseSeconds > 0 {
			return p.leaseSeconds
		}
	case KeyLeaseSweepSeconds:
		if p.leaseSweepSeconds > 0 {
			return p.leaseSweepSeconds
		}
	case KeyWorkerStaleSeconds:
		if p.workerStaleSeconds > 0 {
			return p.workerStaleSeconds
		}
	case KeyRedisClaimMinIdleSeconds:
		if p.redisClaimMinIdleSeconds > 0 {
			return p.redisClaimMinIdleSeconds
		}
	case KeyRedisClaimBatch:
		if p.redisClaimBatch > 0 {
			return p.redisClaimBatch
		}
	case KeyDockerPidsLimit:
		if p.dockerPidsLimit > 0 {
			return p.dockerPidsLimit
		}
	}
	return defaultVal
}

func (p *FlagEnvProvider) GetBool(key string, defaultVal bool) bool {
	switch key {
	case KeyDockerNoNetwork:
		return p.dockerNoNetwork
	default:
		return defaultVal
	}
}

func nonEmpty(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

func normalizeChoice(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
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

func envOrDefaultBool(key string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// MapProvider is used in tests and embedding scenarios.
type MapProvider struct {
	Strings map[string]string
	Ints    map[string]int
	Bools   map[string]bool
}

func (m MapProvider) GetString(key string, defaultVal string) string {
	if m.Strings == nil {
		return defaultVal
	}
	if v, ok := m.Strings[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

func (m MapProvider) GetInt(key string, defaultVal int) int {
	if m.Ints == nil {
		return defaultVal
	}
	if v, ok := m.Ints[key]; ok {
		return v
	}
	return defaultVal
}

func (m MapProvider) GetBool(key string, defaultVal bool) bool {
	if m.Bools == nil {
		return defaultVal
	}
	if v, ok := m.Bools[key]; ok {
		return v
	}
	return defaultVal
}
