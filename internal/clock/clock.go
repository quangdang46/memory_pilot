// Package clock is the only place time is read (module-contracts §1).
package clock

import "time"

type Clock interface {
	Now() time.Time
	Monotonic() time.Duration
	Since(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type real struct{}

func Real() Clock { return real{} }

func (real) Now() time.Time                         { return time.Now() }
func (real) Monotonic() time.Duration               { return time.Since(processStart) }
func (real) Since(t time.Time) time.Duration        { return time.Since(t) }
func (real) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (real) NewTicker(d time.Duration) Ticker       { return realTicker{time.NewTicker(d)} }

var processStart = time.Now()

type realTicker struct{ t *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.t.C }
func (t realTicker) Stop()               { t.t.Stop() }

// Fake is a manually advanced clock for tests.
type Fake struct {
	now   time.Time
	mono  time.Duration
	ticks map[time.Duration]*fakeTicker
}

func NewFake(start time.Time) *Fake {
	return &Fake{now: start, ticks: map[time.Duration]*fakeTicker{}}
}

func (f *Fake) Now() time.Time                  { return f.now }
func (f *Fake) Monotonic() time.Duration        { return f.mono }
func (f *Fake) Since(t time.Time) time.Duration { return f.now.Sub(t) }
func (f *Fake) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() { <-f.Advance(d); ch <- f.now }()
	return ch
}

func (f *Fake) Advance(d time.Duration) <-chan time.Time {
	f.now = f.now.Add(d)
	f.mono += d
	out := make(chan time.Time, 8)
	for period, t := range f.ticks {
		// fire ticks whose period elapsed (at least once)
		n := int(d / period)
		if n == 0 && d >= period {
			n = 1
		}
		for i := 0; i < max(n, 0); i++ {
			select {
			case t.c <- f.now:
			default:
			}
		}
	}
	close(out)
	return out
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	t := &fakeTicker{c: make(chan time.Time, 8), period: d}
	f.ticks[d] = t
	return t
}

type fakeTicker struct {
	c      chan time.Time
	period time.Duration
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }
func (t *fakeTicker) Stop()               {}
