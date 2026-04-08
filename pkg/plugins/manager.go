package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Manager handles dynamic plugin registration and policy checks.
type Manager struct {
	mu sync.RWMutex

	allowlist map[string]struct{}
	pubKeys   map[string]string
	plugins   map[string]Manifest
}

// NewManager creates plugin manager with allowlist and optional per-plugin public keys.
func NewManager(allowlist []string, publicKeys map[string]string) *Manager {
	aw := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			aw[trimmed] = struct{}{}
		}
	}
	pk := make(map[string]string, len(publicKeys))
	for k, v := range publicKeys {
		pk[k] = v
	}
	return &Manager{allowlist: aw, pubKeys: pk, plugins: make(map[string]Manifest)}
}

// Register validates and registers plugin manifest.
func (m *Manager) Register(manifest Manifest) error {
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("plugin name is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("plugin version is required")
	}
	if strings.TrimSpace(manifest.Entrypoint) == "" {
		return fmt.Errorf("plugin entrypoint is required")
	}

	m.mu.RLock()
	_, allowed := m.allowlist[manifest.Name]
	pub, hasKey := m.pubKeys[manifest.Name]
	m.mu.RUnlock()
	if len(m.allowlist) > 0 && !allowed {
		return fmt.Errorf("plugin %q is not in allowlist", manifest.Name)
	}
	if hasKey {
		if err := manifest.VerifySignature(pub); err != nil {
			return err
		}
	} else if strings.TrimSpace(manifest.Signature) == "" {
		return fmt.Errorf("plugin %q signature required when no public key registry entry", manifest.Name)
	}

	m.mu.Lock()
	m.plugins[manifest.Name] = manifest
	m.mu.Unlock()
	return nil
}

// RegisterFromFile registers plugin manifest from JSON file.
func (m *Manager) RegisterFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var mf Manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return err
	}
	return m.Register(mf)
}

// Unregister removes plugin registration.
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	delete(m.plugins, name)
	m.mu.Unlock()
}

// Get returns manifest by name.
func (m *Manager) Get(name string) (Manifest, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mf, ok := m.plugins[name]
	return mf, ok
}

// List returns sorted plugin manifests.
func (m *Manager) List() []Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Manifest, 0, len(m.plugins))
	for _, mf := range m.plugins {
		out = append(out, mf)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out
}
