package csvflush

import "io"

func PlantDropLastBucket(c *Config) { c.plant = plantDropLastBucket }

func PlantStaleValue(c *Config) { c.plant = plantStaleValue }

func SetCreate(c *Config, f func(path string) (io.WriteCloser, error)) { c.create = f }

func SetGate(c *Config, f func()) { c.gate = f }
