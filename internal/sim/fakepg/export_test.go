package fakepg

import "github.com/nickstrad/mini-gateway/internal/sim"

// PlantDoubleApply enables the test-only canary that applies and logs every write twice.
func PlantDoubleApply(c *Config) { c.plant.doubleApply = true }

// PlantIdleInTx enables the test-only canary that reports ReadyForQuery('I') inside open transactions.
func PlantIdleInTx(c *Config) { c.plant.idleInTx = true }

// PlantSkipClear enables the test-only canary that counts a clear of k without clearing its effect.
func PlantSkipClear(c *Config, k sim.FaultKind) { c.plant.skipClear = k }

// PlantDropWrite enables the test-only canary that silently skips the nth write apply call.
func PlantDropWrite(c *Config, nth int64) { c.plant.dropWrite = nth }

// PlantPhantomWritesFrom enables the test-only canary that logs writes from nth onward without mutating the table.
func PlantPhantomWritesFrom(c *Config, nth int64) { c.plant.phantomFrom = nth }
