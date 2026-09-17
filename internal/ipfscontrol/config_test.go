package ipfscontrol

import (
	"encoding/json"
	"testing"
)

func TestConfigRejectsUnsafeValues(t *testing.T) {
	valid := Config{Provider: "internal", BudgetBytes: 20 << 30, MinFreeBytes: 20 << 30, CopyBytesPerSecond: 2 << 20, Workers: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"provider", func(c *Config) { c.Provider = "docker" }},
		{"budget-small", func(c *Config) { c.BudgetBytes = (1 << 20) - 1 }},
		{"budget-large", func(c *Config) { c.BudgetBytes = (1 << 50) + 1 }},
		{"free-negative", func(c *Config) { c.MinFreeBytes = -1 }},
		{"free-large", func(c *Config) { c.MinFreeBytes = (1 << 50) + 1 }},
		{"rate-small", func(c *Config) { c.CopyBytesPerSecond = (64 << 10) - 1 }},
		{"rate-large", func(c *Config) { c.CopyBytesPerSecond = (1 << 30) + 1 }},
		{"workers-zero", func(c *Config) { c.Workers = 0 }},
		{"workers-large", func(c *Config) { c.Workers = 9 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	valid.Provider = "external"
	valid.MinFreeBytes = 0
	valid.Workers = 8
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHostConfigDoesNotExposeApplicationPolicy(t *testing.T) {
	c := Config{Provider: "internal", Enabled: true, AutoPinNew: true, DemandPin: true, BackfillEnabled: true, BudgetBytes: 1 << 30, MinFreeBytes: 2 << 30, CopyBytesPerSecond: 1 << 20, Workers: 2}
	b, err := json.Marshal(c.Host())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got["enabled"] != true || got["workers"] != float64(2) {
		t.Fatalf("unexpected host fields: %s", b)
	}
}
