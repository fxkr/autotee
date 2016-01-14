package autotee

import (
	"time"
)

func WatchChannel(in <-chan *BufPoolElem, timeout time.Duration, notify func()) <-chan *BufPoolElem {
	out := make(chan *BufPoolElem)

	go func() {
		defer close(out)

		ticker := time.NewTicker(timeout)
		defer ticker.Stop()

		slow := false
		notified := false

		for {
			select {

			case buf, more := <-in:
				if !more {
					return
				}
				slow = false
				out <- buf

			case <-ticker.C:
				if !slow {
					slow = true
					continue
				}
				if !notified {
					notified = true
					go notify()
				}
				ticker.Stop()
				ticker.C = nil
			}
		}
	}()

	return out
}
