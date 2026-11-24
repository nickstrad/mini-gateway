package duration

import "time"

const Tick = 3 * time.Second

type Deadline struct {
	At    time.Time
	Every time.Duration
}

func Next(d Deadline, n int64) time.Time {
	step := d.Every*time.Duration(n) + Tick - time.Millisecond
	return d.At.Add(step)
}

func Wire(x time.Duration) int64 {
	return int64(x / time.Microsecond)
}
