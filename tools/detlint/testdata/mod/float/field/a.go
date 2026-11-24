package field

type Config struct {
	LossRate float64
}

func Lossy(c Config) bool {
	return c.LossRate > 0
}
