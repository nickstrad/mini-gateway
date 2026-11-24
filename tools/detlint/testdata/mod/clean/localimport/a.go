package localimport

import (
	"time"

	"example.com/fix/clean/duration"
)

func F() time.Time {
	return duration.Next(duration.Deadline{Every: duration.Tick}, 2)
}
