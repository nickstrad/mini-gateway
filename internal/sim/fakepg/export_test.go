package fakepg

func PlantDoubleApply(c *Config) { c.plant.doubleApply = true }

func PlantIdleInTx(c *Config) { c.plant.idleInTx = true }

func PlantDropWrite(c *Config, nth int64) { c.plant.dropWrite = nth }

func PlantPhantomWritesFrom(c *Config, nth int64) { c.plant.phantomFrom = nth }
