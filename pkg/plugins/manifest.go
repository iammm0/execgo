package plugins

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Manifest describes third-party executor plugin metadata.
type Manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities,omitempty"`
	APIVersion   string   `json:"api_version"`
	Signature    string   `json:"signature"`
	Entrypoint   string   `json:"entrypoint"`
}

// CanonicalPayload returns canonical bytes to be signed.
func (m Manifest) CanonicalPayload() []byte {
	v := struct {
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities,omitempty"`
		APIVersion   string   `json:"api_version"`
		Entrypoint   string   `json:"entrypoint"`
	}{
		Name:         strings.TrimSpace(m.Name),
		Version:      strings.TrimSpace(m.Version),
		Capabilities: append([]string(nil), m.Capabilities...),
		APIVersion:   strings.TrimSpace(m.APIVersion),
		Entrypoint:   strings.TrimSpace(m.Entrypoint),
	}
	b, _ := json.Marshal(v)
	return b
}

// VerifySignature validates Ed25519 signature.
func (m Manifest) VerifySignature(publicKeyBase64 string) error {
	if strings.TrimSpace(m.Signature) == "" {
		return fmt.Errorf("manifest signature is required")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(m.Signature))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(key), m.CanonicalPayload(), sig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
