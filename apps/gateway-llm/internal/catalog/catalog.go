// Package catalog loads the global model catalog (llm_model_profiles)
// from the database with in-memory caching and periodic refresh.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
)

// refreshInterval is how often the catalog re-reads llm_model_profiles.
const refreshInterval = 5 * time.Minute

// Catalog is a cached, validated view of llm_model_profiles.
type Catalog struct {
	pool *pgxpool.Pool

	mu       sync.RWMutex
	profiles map[string][]domain.ModelProfile // keyed by profile_name, sorted by priority
	loadedAt time.Time
}

// New creates a Catalog backed by the given pool.
func New(pool *pgxpool.Pool) *Catalog {
	return &Catalog{pool: pool}
}

// Load fetches all enabled profiles from the database and validates them.
// Any model_id containing "-latest" causes the whole load to fail
// (ADR-021: profiles pin versions, floating aliases are config defects).
func (c *Catalog) Load(ctx context.Context) error {
	rows, err := c.pool.Query(ctx, `
		SELECT profile_name, provider, model_id, priority, capabilities,
		       cost_per_mtok_in, cost_per_mtok_out, enabled
		FROM llm_model_profiles
		WHERE enabled = true
		ORDER BY profile_name, priority, provider, model_id`)
	if err != nil {
		return fmt.Errorf("query llm_model_profiles: %w", err)
	}
	defer rows.Close()

	profiles := map[string][]domain.ModelProfile{}
	for rows.Next() {
		var p domain.ModelProfile
		if err := rows.Scan(&p.ProfileName, &p.Provider, &p.ModelID, &p.Priority,
			&p.Capabilities, &p.CostPerMtokIn, &p.CostPerMtokOut, &p.Enabled); err != nil {
			return fmt.Errorf("scan llm_model_profiles row: %w", err)
		}
		if err := validatePinned(p.ModelID); err != nil {
			return fmt.Errorf("profile %q provider %q model %q: %w", p.ProfileName, p.Provider, p.ModelID, err)
		}
		profiles[p.ProfileName] = append(profiles[p.ProfileName], p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate llm_model_profiles: %w", err)
	}

	for name := range profiles {
		sort.SliceStable(profiles[name], func(i, j int) bool {
			return profiles[name][i].Priority < profiles[name][j].Priority
		})
	}

	c.mu.Lock()
	c.profiles = profiles
	c.loadedAt = time.Now()
	c.mu.Unlock()
	return nil
}

// validatePinned rejects floating aliases such as "-latest".
func validatePinned(modelID string) error {
	if strings.Contains(modelID, "-latest") {
		return domain.ErrFloatingAlias
	}
	return nil
}

// For returns the catalog entries for a profile, ordered by priority.
// It triggers a refresh when the cache is stale (older than refreshInterval).
func (c *Catalog) For(ctx context.Context, profileName string) ([]domain.ModelProfile, error) {
	c.mu.RLock()
	stale := time.Since(c.loadedAt) > refreshInterval
	c.mu.RUnlock()

	if stale {
		if err := c.Load(ctx); err != nil {
			return nil, err
		}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	entries, exists := c.profiles[profileName]
	if !exists || len(entries) == 0 {
		return nil, fmt.Errorf("profile %q: %w", profileName, domain.ErrUnknownProfile)
	}
	// Return a copy to avoid data races on the cached slice.
	out := make([]domain.ModelProfile, len(entries))
	copy(out, entries)
	return out, nil
}

// Capabilities parses the capabilities JSON of a profile entry.
func Capabilities(p domain.ModelProfile) (map[string]any, error) {
	var out map[string]any
	if len(p.Capabilities) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(p.Capabilities, &out); err != nil {
		return nil, fmt.Errorf("parse capabilities: %w", err)
	}
	return out, nil
}
