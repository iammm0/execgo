// ExecGo distributed agent runtime entrypoint.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iammm0/execgo/pkg/config"
	"github.com/iammm0/execgo/pkg/events"
	eventspg "github.com/iammm0/execgo/pkg/events/postgres"
	eventssqlite "github.com/iammm0/execgo/pkg/events/sqlite"
	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/httpserver"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/sandbox"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"
	"github.com/iammm0/execgo/pkg/store/eventsourced"
	"github.com/iammm0/execgo/pkg/taskqueue"
	"github.com/iammm0/execgo/pkg/worker"
)

func main() {
	cfg := config.Load(config.NewFlagEnvProvider())
	logger := observability.NewLogger()
	slog.SetDefault(logger)

	logger.Info("ExecGo starting",
		"addr", cfg.HTTPAddr,
		"grpc_addr", cfg.GRPCAddr,
		"event_store", cfg.EventStoreBackend,
		"queue", cfg.QueueBackend,
		"worker_id", cfg.WorkerID,
		"worker_concurrency", cfg.WorkerConcurrency,
		"sandbox", cfg.SandboxMode,
	)

	executor.RegisterBuiltins()
	logger.Info("executors registered", "types", executor.RegisteredTypes())
	metrics := observability.NewMetrics()
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	obsRuntime, err := observability.InitRuntime(rootCtx, observability.RuntimeConfig{
		ServiceName: "execgo",
	}, metrics, logger)
	if err != nil {
		logger.Error("failed to initialize observability runtime", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := obsRuntime.Shutdown(shutdownCtx); err != nil {
			logger.Warn("observability runtime shutdown failed", "error", err)
		}
	}()

	eventStore, eventCloser, err := initEventStore(cfg, logger)
	if err != nil {
		logger.Error("failed to initialize event store", "error", err)
		os.Exit(1)
	}
	if eventCloser != nil {
		defer eventCloser()
	}

	stateManager, err := eventsourced.NewManager(eventStore, logger)
	if err != nil {
		logger.Error("failed to initialize event sourced state", "error", err)
		os.Exit(1)
	}

	queue, err := initQueue(cfg, logger)
	if err != nil {
		logger.Error("failed to initialize queue", "error", err)
		os.Exit(1)
	}

	sched := scheduler.NewWithQueue(stateManager, metrics, logger, queue)
	sched.Start(rootCtx)

	runner := initSandboxRunner(cfg)
	w := worker.New(worker.Config{
		ID:                cfg.WorkerID,
		Concurrency:       cfg.WorkerConcurrency,
		PollWait:          2 * time.Second,
		LeaseDuration:     time.Duration(cfg.LeaseSeconds) * time.Second,
		HeartbeatInterval: time.Duration(cfg.HeartbeatSeconds) * time.Second,
		RetryBaseBackoff:  100 * time.Millisecond,
		RetryMaxBackoff:   30 * time.Second,
		Runner:            runner,
	}, stateManager, sched, logger)
	w.Start(rootCtx)

	stopGRPC, err := startGRPCServer(cfg.GRPCAddr, stateManager, sched, metrics, logger)
	if err != nil {
		logger.Error("failed to start gRPC server", "error", err)
		os.Exit(1)
	}

	engine := httpserver.NewEngine(stateManager, sched, metrics, logger).
		DisableTrace().
		Use(obsRuntime.HTTPMiddleware()).
		WithPrometheusHandler(obsRuntime.PrometheusHandler())
	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      engine.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		logger.Info("HTTP server listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("shutdown signal received", "signal", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer shutdownCancel()

	logger.Info("stopping HTTP server...")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	if stopGRPC != nil {
		logger.Info("stopping gRPC server...")
		stopGRPC()
	}

	logger.Info("stopping worker...")
	w.Stop()

	logger.Info("stopping scheduler...")
	sched.Stop()

	logger.Info("ExecGo stopped gracefully")
}

func initEventStore(cfg *config.Config, logger *slog.Logger) (events.EventStore, func(), error) {
	switch cfg.EventStoreBackend {
	case "sqlite":
		st, err := eventssqlite.Open(cfg.EventStoreSQLitePath)
		if err != nil {
			return nil, nil, err
		}
		logger.Info("sqlite event store enabled", "path", cfg.EventStoreSQLitePath)
		return st, func() { _ = st.Close() }, nil
	case "postgres":
		st, err := eventspg.Open(cfg.EventStorePostgresDSN)
		if err != nil {
			return nil, nil, err
		}
		logger.Info("postgres event store enabled")
		return st, func() { _ = st.Close() }, nil
	default:
		logger.Info("memory event store enabled")
		return events.NewMemoryStore(), nil, nil
	}
}

func initQueue(cfg *config.Config, logger *slog.Logger) (taskqueue.Queue, error) {
	switch cfg.QueueBackend {
	case "redis":
		q, err := taskqueue.NewRedisQueue(taskqueue.RedisConfig{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
			Prefix:   cfg.RedisPrefix,
			Group:    cfg.RedisGroup,
		})
		if err != nil {
			return nil, err
		}
		logger.Info("redis queue enabled", "addr", cfg.RedisAddr, "prefix", cfg.RedisPrefix)
		return q, nil
	default:
		logger.Info("memory queue enabled")
		return taskqueue.NewMemoryQueue(), nil
	}
}

func initSandboxRunner(cfg *config.Config) sandbox.Runner {
	switch cfg.SandboxMode {
	case "docker":
		return sandbox.NewDockerRunner(sandbox.DockerConfig{
			Image:     cfg.DockerImage,
			CPUs:      cfg.DockerCPUs,
			Memory:    cfg.DockerMemory,
			PidsLimit: cfg.DockerPidsLimit,
			NoNetwork: cfg.DockerNoNetwork,
		})
	default:
		return sandbox.LocalRunner{}
	}
}

var _ store.Store = (*eventsourced.Manager)(nil)
