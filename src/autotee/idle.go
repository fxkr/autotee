package autotee

import (
	"time"
)

func ShowIdleness(d time.Duration) {
	for range time.NewTicker(d).C {
		println("…")
	}
}
