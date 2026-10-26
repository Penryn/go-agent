package presence

import (
	"context"
	"time"
)

func (r *Runtime) loop(interval time.Duration) {
	defer r.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.deps.WorkingMemory.PruneIdle(r.ctx, now)
		}
	}
}

// beginOperation registers intake and model work under the same lock that
// closes admission. Close cannot race with a new WaitGroup.Add.
func (r *Runtime) beginOperation(parent context.Context) (context.Context, func(), error) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(r.ctx, cancel)
	r.wg.Add(1)
	return ctx, func() {
		stop()
		cancel()
		r.wg.Done()
	}, nil
}

func (r *Runtime) beginTurn(parent context.Context, groupID int64) (context.Context, func(), error) {
	ctx, finish, err := r.beginOperation(parent)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, deliberationTimeout)
	r.turnMu.Lock()
	if err := r.ctx.Err(); err != nil {
		r.turnMu.Unlock()
		cancel()
		finish()
		return nil, nil, err
	}
	if previous, ok := r.activeTurns[groupID]; ok {
		previous.cancel()
	}
	r.turnSeq++
	sequence := r.turnSeq
	r.activeTurns[groupID] = inFlightTurn{sequence: sequence, cancel: cancel}
	r.turnMu.Unlock()
	return ctx, func() {
		r.turnMu.Lock()
		if current, ok := r.activeTurns[groupID]; ok && current.sequence == sequence {
			delete(r.activeTurns, groupID)
		}
		r.turnMu.Unlock()
		cancel()
		finish()
	}, nil
}

func (r *Runtime) Close() error {
	r.turnMu.Lock()
	r.cancel()
	for groupID, turn := range r.activeTurns {
		turn.cancel()
		delete(r.activeTurns, groupID)
	}
	r.turnMu.Unlock()
	r.wg.Wait()
	return nil
}
