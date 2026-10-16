package adt

import (
	"context"
	"errors"
	"fmt"
)

// ErrLockReleaseFailed marks a WithObjectLock result whose write succeeded but
// whose UNLOCK did not: the change is in, the lock may still be held.
var ErrLockReleaseFailed = errors.New("write succeeded but releasing the lock failed")

// WithObjectLock runs fn under a lock on lockURL that this call takes and
// releases itself, so the handle never has to survive a model turn (#169).
//
// It follows the same order as every other auto-lock workflow in this package:
// the mutation gate (op, package, transport) runs above the LOCK and marks the
// context, the LOCK carries the caller's transport, the request that goes with
// the write is resolved from the LOCK response under the transportable-edit
// policy (never left for SAP to generate), and a failed fn releases the lock on
// a context detached from the caller's cancellation. fn receives the marked
// context, the handle and the transport to write under.
func (c *Client) WithObjectLock(ctx context.Context, opName, lockURL, transport string,
	fn func(ctx context.Context, lockHandle, transport string) error) error {
	ctx, err := c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    opName,
		ObjectURL: lockURL,
		Transport: transport,
	})
	if err != nil {
		return err
	}

	// No request named on a transportable object: choose one the way the
	// editor would, before the lock (stateless; issue #91), so the LOCK never
	// leaves SAP to generate one on its own.
	trPlan := c.planTransport(ctx, transport, lockURL, "")
	if trPlan != nil && trPlan.Err != nil {
		return trPlan.Err
	}
	lock, err := c.LockObject(ctx, lockURL, "MODIFY", trPlan.lockCorrNr(transport))
	if err != nil {
		return fmt.Errorf("locking %s: %w", lockURL, err)
	}

	unlocked := false
	defer func() {
		if unlocked {
			return
		}
		if unlockErr := c.releaseLockAfterFailure(ctx, lockURL, lock.LockHandle); unlockErr != nil {
			fmt.Fprintf(LogOutput, "[WARN] %s\n", strandedLockAdvice(lockURL, unlockErr))
		}
	}()

	effective, _, err := c.resolveWriteTransportFor(trPlan, transport, lock.CorrNr, opName)
	if err != nil {
		return err
	}
	if err := fn(ctx, lock.LockHandle, effective); err != nil {
		return err
	}
	c.sourceCache.InvalidateByURL(lockURL)

	if err := c.UnlockObject(ctx, lockURL, lock.LockHandle); err != nil {
		// The handle is spent or the unlock failed; either way the deferred
		// release must not send a second UNLOCK for a handle already tried.
		unlocked = true
		return fmt.Errorf("%s: %w: %v — %s", opName, ErrLockReleaseFailed, err, strandedLockAdvice(lockURL, err))
	}
	unlocked = true
	return nil
}
