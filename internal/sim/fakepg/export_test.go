package fakepg

import "github.com/nickstrad/mini-gateway/internal/sim"

func PlantDoubleApply(c *Config) { c.plant.doubleApply = true }

func PlantIdleInTx(c *Config) { c.plant.idleInTx = true }

func PlantSkipClear(c *Config, k sim.FaultKind) { c.plant.skipClear = k }

func PlantDropWrite(c *Config, nth int64) { c.plant.dropWrite = nth }

func PlantPhantomWritesFrom(c *Config, nth int64) { c.plant.phantomFrom = nth }
