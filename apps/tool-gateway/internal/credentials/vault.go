// Package credentials performs credential brokering via Vault/OpenBao.
//
// Agents never receive tokens. The Tool Gateway fetches the secret, injects
// it into the upstream call and redacts it from responses and logs.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	vault "github.com/hashicorp/vault/api"
)

// ErrSecretNotFound is returned when Vault has no value for a requirement.
var ErrSecretNotFound = errors.New("secret not found")

// Requirement describes a secret that a tool needs.
type Requirement struct {
	// Path is the Vault KV path, for example secret/data/github.
	Path string
	// Key is the field inside the secret.
	Key string
	// Env is the environment variable name used by the upstream tool.
	Env string
}

// requirements maps a tool to the secrets it needs.
var requirements = map[string][]Requirement{
	"git.push": {
		{Path: "secret/data/github", Key: "token", Env: "GITHUB_TOKEN"},
	},
	"git.open_pr": {
		{Path: "secret/data/github", Key: "token", Env: "GITHUB_TOKEN"},
	},
	"git.clone": {
		{Path: "secret/data/github", Key: "token", Env: "GITHUB_TOKEN"},
	},
}

// RequirementsFor returns the secrets required by a tool.
func RequirementsFor(tool string) []Requirement {
	reqs := requirements[tool]
	return append([]Requirement(nil), reqs...)
}

// Broker fetches secrets.
type Broker interface {
	Fetch(ctx context.Context, path string) (map[string]string, error)
}

// VaultBroker fetches KV v1/v2 secrets from Vault.
type VaultBroker struct {
	client *vault.Client
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	data    map[string]string
	expires time.Time
}

// NewVaultBroker creates a broker. addr and token are mandatory.
func NewVaultBroker(addr, token string, ttl time.Duration) (*VaultBroker, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("vault address is empty")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("vault token is empty")
	}
	cfg := vault.DefaultConfig()
	cfg.Address = addr
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create vault client: %w", err)
	}
	client.SetToken(token)
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &VaultBroker{
		client: client,
		ttl:    ttl,
		now:    func() time.Time { return time.Now() },
		cache:  make(map[string]cacheEntry),
	}, nil
}

// Fetch reads path from Vault. KV v2 responses are unwrapped from data.data.
func (b *VaultBroker) Fetch(ctx context.Context, path string) (map[string]string, error) {
	if path == "" {
		return nil, errors.New("secret path is empty")
	}

	b.mu.Lock()
	if entry, ok := b.cache[path]; ok && b.now().Before(entry.expires) {
		data := clone(entry.data)
		b.mu.Unlock()
		return data, nil
	}
	b.mu.Unlock()

	secret, err := b.client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read secret %s: %w", path, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("%w: %s", ErrSecretNotFound, path)
	}

	raw := secret.Data
	if nested, ok := secret.Data["data"].(map[string]any); ok {
		raw = nested
	}
	data := make(map[string]string, len(raw))
	for key, value := range raw {
		if s, ok := value.(string); ok {
			data[key] = s
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: %s has no string fields", ErrSecretNotFound, path)
	}

	b.mu.Lock()
	b.cache[path] = cacheEntry{data: clone(data), expires: b.now().Add(b.ttl)}
	b.mu.Unlock()
	return data, nil
}

func clone(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var tokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{10,}`),
}

// Redactor removes known secret values and token-shaped strings from text.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// NewRedactor creates an empty Redactor.
func NewRedactor() *Redactor {
	return &Redactor{}
}

// Add registers secret values to redact. Empty values are ignored.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if v == "" {
			continue
		}
		already := false
		for _, existing := range r.secrets {
			if existing == v {
				already = true
				break
			}
		}
		if !already {
			r.secrets = append(r.secrets, v)
		}
	}
	// Replace longer secrets first so a short prefix cannot mask a longer one.
	sort.Slice(r.secrets, func(i, j int) bool {
		return len(r.secrets[i]) > len(r.secrets[j])
	})
}

// Redact returns s with secrets and token-shaped strings replaced.
func (r *Redactor) Redact(s string) string {
	r.mu.RLock()
	secrets := append([]string(nil), r.secrets...)
	r.mu.RUnlock()

	for _, secret := range secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	for _, pattern := range tokenPatterns {
		s = pattern.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
