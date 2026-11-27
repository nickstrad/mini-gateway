package config

import "github.com/nickstrad/mini-gateway/internal/proxy"

// ProxyCaps exposes proxyCaps to external config_test assertions on engine admission wiring.
func (f *File) ProxyCaps() proxy.Config { return f.proxyCaps() }
