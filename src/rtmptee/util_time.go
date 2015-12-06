package rtmptee

import "time"

type RestartableTimer struct {
	C <-chan time.Time

	duration time.Duration
	timer    *time.Timer
}

func NewRestartableTimer(d time.Duration) RestartableTimer {
	return RestartableTimer{duration: d}
}

func (st *RestartableTimer) Start() {
	if st.timer == nil {
		st.timer = time.NewTimer(st.duration)
		st.C = st.timer.C
	}
}

func (st *RestartableTimer) Stop() {
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
		st.C = nil
	}
}

func (st *RestartableTimer) Restart() {
	if st.timer == nil {
		st.Start()
	}
	st.timer.Reset(st.duration)
}

// Like time.Tick, but the first tick comes immediately.
func TickNow(d time.Duration) <-chan time.Time {
	channel := make(chan time.Time)
	go func() {
		tick := time.Tick(d)
		channel <- time.Now()
		for now := range tick {
			channel <- now
		}
	}()
	return channel
}
