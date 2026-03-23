package plugin

import (
	"athena/internal/config"
)

type PluginHandlers struct {
	cfg *config.Config
}

func NewPluginHandlers(cfg *config.Config) *PluginHandlers {
	return &PluginHandlers{
		cfg: cfg,
	}
}
