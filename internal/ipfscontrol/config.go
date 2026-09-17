// Package ipfscontrol owns the managed public mirror's desired configuration.
// It never gives web processes access to the Docker daemon.
package ipfscontrol

import "fmt"

// Config is one atomic policy document. Delivery remains independently gated by
// delivery_ipfs_enabled. Disabled admission must not stop privacy withdrawals.
type Config struct {
	Provider           string `json:"provider"`
	Enabled            bool   `json:"enabled"`
	AutoPinNew         bool   `json:"auto_pin_new"`
	DemandPin          bool   `json:"demand_pin"`
	BackfillEnabled    bool   `json:"backfill_enabled"`
	BudgetBytes        int64  `json:"budget_bytes"`
	MinFreeBytes       int64  `json:"min_free_bytes"`
	CopyBytesPerSecond int64  `json:"copy_bytes_per_second"`
	Workers            int    `json:"workers"`
}

// HostConfig is the entire operational allowlist accepted by the host manager.
// URLs, images, mount paths and commands deliberately cannot cross this boundary.
type HostConfig struct {
	Enabled            bool  `json:"enabled"`
	BudgetBytes        int64 `json:"budget_bytes"`
	MinFreeBytes       int64 `json:"min_free_bytes"`
	CopyBytesPerSecond int64 `json:"copy_bytes_per_second"`
	Workers            int   `json:"workers"`
}

func (c Config) Host() HostConfig {
	return HostConfig{c.Enabled, c.BudgetBytes, c.MinFreeBytes, c.CopyBytesPerSecond, c.Workers}
}

func (c Config) Validate() error {
	if c.Provider != "internal" && c.Provider != "external" {
		return fmt.Errorf("provider must be internal or external")
	}
	return c.Host().Validate()
}

func (c HostConfig) Validate() error {
	for _, f := range []struct {
		name        string
		n, min, max int64
	}{
		{"budget_bytes", c.BudgetBytes, 1 << 20, 1 << 50},
		{"min_free_bytes", c.MinFreeBytes, 0, 1 << 50},
		{"copy_bytes_per_second", c.CopyBytesPerSecond, 64 << 10, 1 << 30},
		{"workers", int64(c.Workers), 1, 8},
	} {
		if f.n < f.min || f.n > f.max {
			return fmt.Errorf("%s must be between %d and %d", f.name, f.min, f.max)
		}
	}
	return nil
}
