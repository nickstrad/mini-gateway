package enginesrc

import (
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func Register(c *obs.Collector, e *proxy.Engine, shell func() proxy.ShellSnapshot) error {
	return nil
}

func HealthRules() []obs.Rule { return nil }
