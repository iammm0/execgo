package unit_test

import (
	"strings"
	"testing"

	"github.com/iammm0/execgo/pkg/config"
)

func TestConfigLoad_Defaults(t *testing.T) {
	cfg := config.Load(config.MapProvider{})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate defaults: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr=%q want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.GRPCAddr != ":50051" {
		t.Fatalf("GRPCAddr=%q want %q", cfg.GRPCAddr, ":50051")
	}
	if cfg.DataDir != "data" {
		t.Fatalf("DataDir=%q want %q", cfg.DataDir, "data")
	}
	if cfg.MaxConcurrency != 10 {
		t.Fatalf("MaxConcurrency=%d want %d", cfg.MaxConcurrency, 10)
	}
	if cfg.ShutdownTimeout != 15 {
		t.Fatalf("ShutdownTimeout=%d want %d", cfg.ShutdownTimeout, 15)
	}
}

func TestConfigLoad_MapProviderOverrides(t *testing.T) {
	cfg := config.Load(config.MapProvider{
		Strings: map[string]string{
			config.KeyHTTPAddr: ":18080",
			config.KeyGRPCAddr: ":15051",
			config.KeyDataDir:  "/tmp/execgo-data",
		},
		Ints: map[string]int{
			config.KeyMaxConcurrency:  32,
			config.KeyShutdownTimeout: 60,
		},
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate overrides: %v", err)
	}

	if cfg.HTTPAddr != ":18080" {
		t.Fatalf("HTTPAddr=%q want %q", cfg.HTTPAddr, ":18080")
	}
	if cfg.GRPCAddr != ":15051" {
		t.Fatalf("GRPCAddr=%q want %q", cfg.GRPCAddr, ":15051")
	}
	if cfg.DataDir != "/tmp/execgo-data" {
		t.Fatalf("DataDir=%q want %q", cfg.DataDir, "/tmp/execgo-data")
	}
	if cfg.MaxConcurrency != 32 {
		t.Fatalf("MaxConcurrency=%d want %d", cfg.MaxConcurrency, 32)
	}
	if cfg.ShutdownTimeout != 60 {
		t.Fatalf("ShutdownTimeout=%d want %d", cfg.ShutdownTimeout, 60)
	}
}

func TestConfigValidate_DistributedRuntimeBackends(t *testing.T) {
	cases := []struct {
		name       string
		provider   config.MapProvider
		wantErrSub string
	}{
		{
			name: "redis queue requires address",
			provider: config.MapProvider{Strings: map[string]string{
				config.KeyQueueBackend: "redis",
			}},
			wantErrSub: "redis queue requires",
		},
		{
			name: "postgres event store requires dsn",
			provider: config.MapProvider{Strings: map[string]string{
				config.KeyEventStoreBackend: "postgres",
			}},
			wantErrSub: "postgres event store requires",
		},
		{
			name: "unknown event store is rejected",
			provider: config.MapProvider{Strings: map[string]string{
				config.KeyEventStoreBackend: "etcd",
			}},
			wantErrSub: "unsupported event store backend",
		},
		{
			name: "valid redis and postgres config",
			provider: config.MapProvider{Strings: map[string]string{
				config.KeyEventStoreBackend:     " POSTGRES ",
				config.KeyEventStorePostgresDSN: "postgres://user:pass@127.0.0.1:5432/execgo?sslmode=disable",
				config.KeyQueueBackend:          " Redis ",
				config.KeyRedisAddr:             "127.0.0.1:6379",
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Load(tc.provider)
			err := cfg.Validate()
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("Validate() error=%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error=nil want substring %q", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("Validate() error=%q want substring %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}
