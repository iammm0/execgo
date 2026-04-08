// Package observability 提供结构化日志、请求追踪和指标收集
// provides structured logging, request tracing, and metrics collection.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// ----------------------------------------------------------------
// Trace ID 追踪标识
// ----------------------------------------------------------------

type ctxKey string

const traceIDKey ctxKey = "trace_id"

const tracerName = "execgo/runtime"

// NewTraceID 生成 16 字节十六进制追踪 ID / generates a 16-byte hex trace ID.
func NewTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// WithTraceID 将 traceID 注入 context / injects traceID into context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceIDFromContext 从 context 中提取 traceID / extracts traceID from context.
func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// ----------------------------------------------------------------
// Logger 结构化日志
// ----------------------------------------------------------------

// NewLogger 创建带 JSON 格式的 slog 日志器 / creates a JSON slog logger.
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// L 返回携带 traceID 的子日志器 / returns a sub-logger with traceID.
func L(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if tid := TraceIDFromContext(ctx); tid != "" {
		return logger.With("trace_id", tid)
	}
	return logger
}

// ----------------------------------------------------------------
// Middleware HTTP 中间件
// ----------------------------------------------------------------

// TraceMiddleware 为每个请求注入 traceID / injects traceID for each request.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			if spanCtx := trace.SpanContextFromContext(r.Context()); spanCtx.IsValid() {
				traceID = spanCtx.TraceID().String()
			}
		}
		if traceID == "" {
			traceID = NewTraceID()
		}
		ctx := WithTraceID(r.Context(), traceID)
		w.Header().Set("X-Trace-ID", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ----------------------------------------------------------------
// Metrics 内存指标
// ----------------------------------------------------------------

// Metrics 全局指标收集器 / global metrics collector.
type Metrics struct {
	TasksTotal     atomic.Int64
	TasksRunning   atomic.Int64
	TasksSucceeded atomic.Int64
	TasksFailed    atomic.Int64

	mu     sync.RWMutex
	ByType map[string]*atomic.Int64
}

// NewMetrics 创建指标实例 / creates a metrics instance.
func NewMetrics() *Metrics {
	return &Metrics{
		ByType: make(map[string]*atomic.Int64),
	}
}

// IncType 增加某类型的计数 / increments a type counter.
func (m *Metrics) IncType(taskType string) {
	m.mu.RLock()
	counter, ok := m.ByType[taskType]
	m.mu.RUnlock()

	if !ok {
		m.mu.Lock()
		counter, ok = m.ByType[taskType]
		if !ok {
			counter = &atomic.Int64{}
			m.ByType[taskType] = counter
		}
		m.mu.Unlock()
	}
	counter.Add(1)
}

// Snapshot 返回当前指标快照 / returns a metrics snapshot.
func (m *Metrics) Snapshot() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byType := make(map[string]int64, len(m.ByType))
	for k, v := range m.ByType {
		byType[k] = v.Load()
	}

	return byType
}

// RuntimeConfig controls OpenTelemetry runtime initialization.
type RuntimeConfig struct {
	ServiceName string
}

// Runtime wires OTel providers and HTTP integrations.
type Runtime struct {
	logger *slog.Logger

	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	promRegistry   *prometheus.Registry
	promHandler    http.Handler
}

// InitRuntime initializes OpenTelemetry providers and bridges legacy in-memory
// metrics into a Prometheus scrape endpoint.
func InitRuntime(_ context.Context, cfg RuntimeConfig, m *Metrics, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "execgo"
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, err
	}

	promRegistry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(
		otelprom.WithRegisterer(promRegistry),
		otelprom.WithResourceAsConstantLabels(func(kv attribute.KeyValue) bool {
			return kv.Key == semconv.ServiceNameKey
		}),
	)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1.0))),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	meter := meterProvider.Meter(tracerName)
	if err := registerLegacyMetricsBridge(meter, m); err != nil {
		return nil, err
	}

	logger.Info("observability runtime initialized", "service_name", cfg.ServiceName)
	return &Runtime{
		logger:         logger,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		promRegistry:   promRegistry,
		promHandler:    promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}),
	}, nil
}

// HTTPMiddleware returns OTel HTTP middleware and maintains X-Trace-ID
// compatibility for existing clients/logging.
func (r *Runtime) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if next == nil {
			next = http.NotFoundHandler()
		}
		traceAware := TraceMiddleware(next)
		return otelhttp.NewHandler(traceAware, "execgo.http.request")
	}
}

// PrometheusHandler returns a scrape handler for OTel+legacy runtime metrics.
func (r *Runtime) PrometheusHandler() http.Handler {
	if r == nil {
		return nil
	}
	return r.promHandler
}

// Shutdown flushes and stops telemetry providers.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var err error
	if r.meterProvider != nil {
		err = errors.Join(err, r.meterProvider.Shutdown(ctx))
	}
	if r.tracerProvider != nil {
		err = errors.Join(err, r.tracerProvider.Shutdown(ctx))
	}
	return err
}

// StartSpan starts a named span from the global provider.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name, opts...)
}

func registerLegacyMetricsBridge(meter metric.Meter, m *Metrics) error {
	if m == nil {
		return nil
	}

	tasksTotal, err := meter.Int64ObservableGauge(
		"execgo_tasks_total",
		metric.WithDescription("Total accepted tasks"),
	)
	if err != nil {
		return err
	}
	tasksRunning, err := meter.Int64ObservableGauge(
		"execgo_tasks_running",
		metric.WithDescription("Currently running tasks"),
	)
	if err != nil {
		return err
	}
	tasksSucceeded, err := meter.Int64ObservableGauge(
		"execgo_tasks_succeeded_total",
		metric.WithDescription("Total succeeded tasks"),
	)
	if err != nil {
		return err
	}
	tasksFailed, err := meter.Int64ObservableGauge(
		"execgo_tasks_failed_total",
		metric.WithDescription("Total failed tasks"),
	)
	if err != nil {
		return err
	}
	tasksByType, err := meter.Int64ObservableGauge(
		"execgo_tasks_by_type_total",
		metric.WithDescription("Tasks grouped by task type"),
	)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(tasksTotal, m.TasksTotal.Load())
		o.ObserveInt64(tasksRunning, m.TasksRunning.Load())
		o.ObserveInt64(tasksSucceeded, m.TasksSucceeded.Load())
		o.ObserveInt64(tasksFailed, m.TasksFailed.Load())
		for taskType, count := range m.Snapshot() {
			o.ObserveInt64(tasksByType, count, metric.WithAttributes(attribute.String("task_type", taskType)))
		}
		return nil
	}, tasksTotal, tasksRunning, tasksSucceeded, tasksFailed, tasksByType)

	return err
}
