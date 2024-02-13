// Package retry is an exploration use of Go 1.22's experimental
// [range functions] to provide a "retry loop". It was inspired by Xe
// Iaso's post [I wish Go had a retry block].
//
// This code is a proof-of-concept only; it's received only light testing
// and is not intended for production use. Because it uses an experimental
// Go feature you must set GOEXPERIMENT=rangefunc when building.
//
// [range functions]: https://go.dev/wiki/RangefuncExperiment
// [I wish Go had a retry block]: https://xeiaso.net/blog/2024/retry-block/
package retry

import (
	"cmp"
	"context"
	"errors"
	"iter"
	"math/rand/v2"
	"time"
)

const (
	// DefaultRetries is the default value used for [Retry.Retries]
	// if no value is provided.
	DefaultRetries = 3

	// DefaultBackoff is the default value used for [Retry.Backoff]
	// if not value is provided.
	DefaultBackoff = time.Second * 5
)

// ErrRetriesExceeded is the error value returned if all attempted
// retries fail.
var ErrRetriesExceeded = errors.New("retry count exceeded")

type yieldFunc = func(context.Context) bool

// Retry controls the behavior of a retry loop.
//
// A Retry object should not be reused.
type Retry struct {
	// Retries is the number of times a failed request will be retried
	// before the request is considered failed.
	Retries int

	// Timeout is an optional time limit applied to each yielded context.
	Timeout time.Duration

	// Backoff is the initial cooldown delay applied between a failed
	// request and the next retry.
	Backoff time.Duration

	// TODO: remove this, instead pass as an `err *error` argument into
	// Retry()? This would avoid changing the Retry object.
	err error
}

// Err returns the error result from a retried operation.
//
// It returns [nil] if any request succeeded, [ErrRetriesExceeded] if
// all retry attempts failed, or the context error if its done channel
// was signaled.
func (r *Retry) Err() error {
	return r.err
}

// Retry returns a [range function] to retry a fallible request.
//
// The returned [iter.Seq] should be used as the range in a for loop. Each
// iteration through the loop is passed a child [context.Context] of ctx.
// A randomized exponential backoff is applied between any failed request
// and before attempting to retry the loop.
//
// The caller should ensure that any blocking I/O is bound to the yielded
// context. Failure to do so can cause the loop to hang.
//
// [range function]: https://go.dev/wiki/RangefuncExperiment
func (r *Retry) Retry(ctx context.Context) iter.Seq[context.Context] {
	retries := cmp.Or(r.Retries, DefaultRetries)
	backoff := cmp.Or(r.Backoff, DefaultBackoff)
	timeout := r.Timeout

	if retries < 0 || timeout < 0 || backoff < 0 {
		panic("invalid retry parameters")
	}

	return func(yield func(context.Context) bool) {
		r.err = retryLoop(ctx, retries, timeout, backoff, yield)
	}
}

// retryLoop is the core of the retry [iter.Seq] rangfunc.
func retryLoop(ctx context.Context, retries int, timeout, backoff time.Duration, yield yieldFunc) error {
	// Attempt the request for the first loop iteration. If it
	// succeeds then no fancy behavior is necessary, simply return
	// nil to signal the operation succeeded.
	if !yieldWithChildContext(ctx, timeout, yield) {
		return nil
	}

	// Configure the initial failure backoff.
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	// Counting the initial request, we'll iterate the loop at most
	// retries+1 times.
	for _ = range retries {
		select {
		case <-ctx.Done():
			// The parent context has exited.
			return ctx.Err()

		case <-timer.C:
			// The backoff cooldown completed, retry the
			// request. If it succeeds, signal success (and
			// squash any previous errors).
			if !yieldWithChildContext(ctx, timeout, yield) {
				return nil
			}

			// Compute the randomized exponential backoff:
			//    b′ = bⁿ, n ∈ [1¼, 1¾)
			backoff += backoff/4 + rand.N(backoff/2)
			timer.Reset(backoff)
		}
	}

	// Every retry failed, signal the error.
	return ErrRetriesExceeded
}

func yieldWithChildContext(ctx context.Context, timeout time.Duration, yield yieldFunc) bool {
	var cancel context.CancelFunc
	if timeout == 0 {
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}

	defer cancel()
	return yield(ctx)
}
