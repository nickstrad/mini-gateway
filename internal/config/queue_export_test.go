package config

import "github.com/nickstrad/mini-gateway/internal/proxy"

func (f *File) ProxyCaps() proxy.Config { return f.proxyCaps() }
