// ExecGo — 极简 AI 执行引擎 / Minimal AI Execution Engine.
//
// 为 AI Agent 提供任务提交、DAG 调度、并发执行和可观测性的 HTTP 服务。
// An HTTP service providing task submission, DAG scheduling, concurrent execution,
// and observability for AI agents.
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
	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/httpserver"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store/jsonfile"
)

func main() {
	cfg := config.Load(config.NewFlagEnvProvider())

	logger := observability.NewLogger()
	slog.SetDefault(logger)

	logger.Info("ExecGo starting",
		"addr", cfg.HTTPAddr,
		"grpc_addr", cfg.GRPCAddr,
		"data_dir", cfg.DataDir,
		"max_concurrency", cfg.MaxConcurrency,
	)

	executor.RegisterBuiltins()
	logger.Info("executors registered", "types", executor.RegisteredTypes())

	metrics := observability.NewMetrics()

	sm, err := jsonfile.NewManager(cfg.DataDir, logger)
	if err != nil {
		logger.Error("failed to initialize state manager", "error", err)
		os.Exit(1)
	}

	persistStop := make(chan struct{})
	sm.StartPeriodicPersist(30*time.Second, persistStop)

	sched := scheduler.New(sm, metrics, logger, cfg.MaxConcurrency)
	sched.Start(context.Background())

	stopGRPC, err := startGRPCServer(cfg.GRPCAddr, sm, sched, metrics, logger)
	if err != nil {
		logger.Error("failed to start gRPC server", "error", err)
		os.Exit(1)
	}

	engine := httpserver.NewEngine(sm, sched, metrics, logger)

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

	logger.Info("stopping scheduler...")
	sched.Stop()

	logger.Info("persisting final state...")
	close(persistStop)
	time.Sleep(100 * time.Millisecond)

	logger.Info("ExecGo stopped gracefully")
}
