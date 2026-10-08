package systemhealth

import (
	"context"
	"sync"
	"time"
)

// Collector persists real sampler observations on the fixed local cadence.
// Missed ticks are not replayed and downtime never creates artificial history.
type Collector struct {
	store     *Store
	sampler   *Sampler
	mu        sync.Mutex
	lastError error
}

func NewCollector(store *Store, sampler *Sampler) (*Collector, error) {
	if store == nil || sampler == nil || len(store.targets) != len(sampler.storage) {
		return nil, problem(500, "invalid_configuration")
	}
	for _, target := range sampler.storage {
		expected, ok := store.byID[target.ID]
		if !ok || expected != target {
			return nil, problem(500, "invalid_configuration")
		}
	}
	return &Collector{store: store, sampler: sampler}, nil
}
func (c *Collector) Collect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return safeError(err)
	}
	err := c.store.Persist(ctx, c.sampler.Sample())
	c.mu.Lock()
	c.lastError = err
	c.mu.Unlock()
	return err
}
func (c *Collector) LastError() error { c.mu.Lock(); defer c.mu.Unlock(); return c.lastError }
func (c *Collector) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_ = c.Collect(ctx)
	ticker := time.NewTicker(SampleCadence)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = c.Collect(ctx)
		}
	}
}
