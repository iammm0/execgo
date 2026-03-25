//go:build !grpc

package main

import (
	"log/slog"

	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"
)

// startGRPCServer 默认在未启用 `-tags grpc` 时不会启动任何 gRPC 服务。
func startGRPCServer(addr string, st store.Store, sched *scheduler.Scheduler, metrics *observability.Metrics, logger *slog.Logger) (func(), error) {
	_ = addr
	_ = st
	_ = sched
	_ = metrics
	_ = logger
	return nil, nil
}

