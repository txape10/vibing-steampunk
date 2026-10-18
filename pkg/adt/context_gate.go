package adt

import (
	"context"
	"sync"
)

// contextGate is a readers-writer gate with a FIFO queue of writers.
//
// A stateless request that is allowed to end the stateful ADT context holds
// the gate shared; a request that goes into the context holds it exclusively,
// so those go one at a time (the ICM answers a second concurrent request in a
// busy context with 400). Readers never wait: tryShared fails while a writer
// holds the gate or is queued for it, so readers cannot starve a writer.
// Writers queue in arrival order and give up with their context.
//
// The zero value never blocks anything, so a Transport built as a literal
// keeps working; see newContextGate.
type contextGate struct {
	s *gateState
}

type gateState struct {
	mu      sync.Mutex
	readers int
	writer  bool
	queue   []chan struct{}
}

func newContextGate() contextGate { return contextGate{s: &gateState{}} }

// tryShared takes the gate shared if no writer holds it or waits for it.
func (g contextGate) tryShared() bool {
	if g.s == nil {
		return true
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if g.s.writer || len(g.s.queue) > 0 {
		return false
	}
	g.s.readers++
	return true
}

func (g contextGate) releaseShared() {
	if g.s == nil {
		return
	}
	g.s.mu.Lock()
	g.s.readers--
	g.s.handoff()
	g.s.mu.Unlock()
}

// lock takes the gate exclusively, or gives up with ctx's error. A gate handed
// over just as ctx ended is given back: the caller is not to go on.
func (g contextGate) lock(ctx context.Context) error {
	if g.s == nil {
		return nil
	}
	g.s.mu.Lock()
	if !g.s.writer && g.s.readers == 0 && len(g.s.queue) == 0 {
		g.s.writer = true
		g.s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			g.unlock()
			return err
		}
		return nil
	}
	turn := make(chan struct{}, 1)
	g.s.queue = append(g.s.queue, turn)
	g.s.mu.Unlock()

	select {
	case <-turn:
		if err := ctx.Err(); err != nil {
			g.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		g.s.mu.Lock()
		for i, w := range g.s.queue {
			if w == turn {
				g.s.queue = append(g.s.queue[:i], g.s.queue[i+1:]...)
				g.s.handoff()
				g.s.mu.Unlock()
				return ctx.Err()
			}
		}
		g.s.mu.Unlock()
		// The turn was granted while we gave up: take it, then pass it on.
		<-turn
		g.unlock()
		return ctx.Err()
	}
}

func (g contextGate) unlock() {
	if g.s == nil {
		return
	}
	g.s.mu.Lock()
	g.s.writer = false
	g.s.handoff()
	g.s.mu.Unlock()
}

// handoff gives the gate to the first queued writer if it is free. The caller
// holds mu.
func (s *gateState) handoff() {
	if s.writer || s.readers > 0 || len(s.queue) == 0 {
		return
	}
	turn := s.queue[0]
	s.queue = s.queue[1:]
	s.writer = true
	turn <- struct{}{}
}
