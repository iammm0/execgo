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

	"github.com/iammm0/execgo/internal/api"
	"github.com/iammm0/execgo/internal/config"
	"github.com/iammm0/execgo/internal/executor"
	"github.com/iammm0/execgo/internal/observability"
	"github.com/iammm0/execgo/internal/scheduler"
	"github.com/iammm0/execgo/internal/state"
)

func main() {
	// 初始化配置 / initialize configuration
	cfg := config.Load()

	// 初始化日志 / initialize logger
	logger := observability.NewLogger()
	slog.SetDefault(logger)

	logger.Info("ExecGo starting",
		"addr", cfg.HTTPAddr,
		"data_dir", cfg.DataDir,
		"max_concurrency", cfg.MaxConcurrency,
	)

	// 注册内置执行器 / register built-in executors
	executor.RegisterBuiltins()
	logger.Info("executors registered", "types", executor.RegisteredTypes())

	// 初始化指标 / initialize metrics
	metrics := observability.NewMetrics()

	// 初始化状态管理器 / initialize state manager
	sm, err := state.NewManager(cfg.DataDir, logger)
	if err != nil {
		logger.Error("failed to initialize state manager", "error", err)
		os.Exit(1)
	}

	// 启动定期持久化 / start periodic persistence
	persistStop := make(chan struct{})
	sm.StartPeriodicPersist(30*time.Second, persistStop)

	// 初始化并启动调度器 / initialize and start scheduler
	sched := scheduler.New(sm, metrics, logger, cfg.MaxConcurrency)
	sched.Start(context.Background())

	// 初始化 API 服务器 / initialize API server
	srv := api.NewServer(sm, sched, metrics, logger)

	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动 HTTP 服务 / start HTTP server
	go func() {
		logger.Info("HTTP server listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// 优雅关闭 / graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("shutdown signal received", "signal", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer shutdownCancel()

	// 按顺序关闭各组件 / shut down components in order
	logger.Info("stopping HTTP server...")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	logger.Info("stopping scheduler...")
	sched.Stop()

	logger.Info("persisting final state...")
	close(persistStop)
	time.Sleep(100 * time.Millisecond) // 等待最终持久化完成 / wait for final persist

	logger.Info("ExecGo stopped gracefully")
}
