package unit_test

import (
	"testing"

	"github.com/iammm0/execgo/pkg/config"
)

func TestConfigLoad_Defaults(t *testing.T) {
	cfg := config.Load(config.MapProvider{})

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
