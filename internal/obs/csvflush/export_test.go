package csvflush

import "io"

// PlantDropLastBucket enables the test-only canary switch that omits the last
// histogram's final bucket row on each flush so RoundTrip can prove it catches it.
func PlantDropLastBucket(c *Config) { c.plant = plantDropLastBucket }

// PlantStaleValue enables the test-only canary switch that writes the previous
// flush's value at each point index so RoundTrip can prove it catches stale rows.
func PlantStaleValue(c *Config) { c.plant = plantStaleValue }

// SetCreate overrides file creation in tests to inject disk-full or I/O errors.
func SetCreate(c *Config, f func(path string) (io.WriteCloser, error)) { c.create = f }

// SetGate installs a hook called by the async writer goroutine before each
// write so tests can hold the writer and fill the bounded queue deterministically.
func SetGate(c *Config, f func()) { c.gate = f }
