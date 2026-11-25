package fakepg

func PlantDoubleApply(c *Config) { c.plant.doubleApply = true }

func PlantIdleInTx(c *Config) { c.plant.idleInTx = true }
