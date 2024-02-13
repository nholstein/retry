# Retry Blocks in Go using rangefunc

This is an example package demonstrating how Go 1.22's [rangfunc
experiment](https://go.dev/wiki/RangefuncExperiment) can be used to
implement retry blocks; automatic request retries using native Go scopes
and syntax.

It is inspired by Xe Iaso's post [I wish Go had a retry
block](https://xeiaso.net/blog/2024/retry-block/).

## How it works

A `Retry` function implements a rangefunc. You call the function with a
[`Context`](https://pkg.go.dev/context#Context), each iteration through
the `for` loop is passed a child context with a timeout applied.

```Go
var r retry.Retry

for childCtx := r.Retry(ctx) {
	// perform the fallible request
	err := someFallibleRequest(childCtx)
	if err == nil {
		// success, exit the loop
		break
	}
}

return r.Err()
```

For a complete example, see [`ExampleRetry` in retry_test.go](./retry_test.go#L29).

One advantage of this approach over pre-1.22 rangefunc is that errors
can be handled idiomatically within the for loop. For example, the
popular cenkalti/backoff package requires [wrapping errors](https://pkg.go.dev/github.com/cenkalti/backoff/v4#PermanentError)
which should not be retried:

```Go
err := backoff.Retry(func() error {
	err := someFallibleRequest(ctx)
	if errorIsNotRecoverable(err) {
		return backoff.Permanent(err)
	}
	return nil
}, NewExponentialBackOff())
if err != nil {
	return err
}
```

Using rangefunc the code can directly return such an error:

```Go
var r retry.Retry

for childCtx := r.Retry(ctx) {
	err := someFallibleRequest(childCtx)
	select {
	case errorIsNotRecoverable(err):
		return err

	case err == nil:
		return nil
	}
}

return r.Err()
```

## Thoughts on the rangefunc proposal

Generally, very positive! Kudos for a design which has the flexibility
to be used for iterating over collections can also be used to provide
fairly aribitrary request retries and backoffs. Other new additions in
Go 1.22 (range-over-int, rand/v2.N) made parts of the implementation
nicer as well.

One limitation I noticed is that there is very limited ability for
feedback to flow from the `for` loop body back into the rangefunc. In
the current design, the only feedback is a boolean true/false to exit
the loop.

Look at the example of making an HTTP GET request:

```Go
var r Retry

for childCtx := range r.Retry(ctx) {
	req, err := http.NewRequestWithContext(childCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	rsp, err := http.DefaultClient.Do(req)
	switch {
	case err != nil:
		// ???
		continue

	case rsp.StatusCode < 200 || rsp.StatusCode >= 300:
		// ???
		continue

	default:
		return nil
	}
}

return r.Err()
```

An error returned from `NewRequestWithContext` is a fatal error, it
signifies a flaw within the URL. Such an error should not be retried,
and should be returned immediately.

However an error from `Do` or a bad HTTP status code should cause the
request to be retried. With the current propoal these errors are lost.
If the maximum number of retries is reached without success then a
generic `ErrRetriesExceeded` is returned.

One potential solution would be to add an optional secondary return
value to the `yield` function¹:

```Go
package iter

type Seq[V any] func(yield func(V) bool)
type SeqReturn[V, R any] func(yield func(V) (R, bool))
```

The `continue` statement would be extended to allow it to accept a
single value:

```Go
var r Retry

for childCtx := range r.Retry(ctx) {
	req, err := http.NewRequestWithContext(childCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	rsp, err := http.DefaultClient.Do(req)
	switch {
	case err != nil:
		continue err

	case rsp.StatusCode < 200 || rsp.StatusCode >= 300:
		continue fmt.Errorf("HTTP status failed: %d", rsp.StatusCode)

	default:
		return nil
	}
}

return r.Err()
```

If no value is passed with a `continue`, or control reaches the end of
the loop and repeats, then the zero value is returned.

This would allow the `Retry` function to accumulate errors from each
request and return them from the `Retry.Err()` function:

```Go
func retryLoop(ctx, retries int, yield func(context.Context) bool) error {
	err, loop := yield(ctx)
	if !loop {
		return nil
	}

	for _ = range retries {
		e, loop := yield(ctx)
		if !loop {
			return nil
		}

		err = errors.Join(err, e)
	}

	return errors.Join(err, ErrRetriesExceeded)
}
```

While this improves the retry use case, it would be a fairly complicated
addition to the proposal and the language for what's likely a fringe
benefit. It's also a backward-compatible change; it could be retrofitted
at a later date if desired.

¹ I don't believe this is an original idea; I have vague memories that
this is possible using `yield` statements in Python/Ruby/Javascript/...
A cursory search didn't show any results. (Yield any results?)
