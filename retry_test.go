package retry_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/nholstein/retry"
)

func testContext(t *testing.T) context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	t.Cleanup(cancel)

	deadline, ok := t.Deadline()
	if ok {
		ctx, cancel = context.WithDeadline(ctx, deadline)
		t.Cleanup(cancel)
	}

	return ctx
}

func ExampleRetry(ctx context.Context, url string) error {
	r := retry.Retry{
		Retries: 2,
		Timeout: time.Second * 5,
		Backoff: time.Second,
	}

	for ctx, cancel := range r.Retry(ctx) {
		// Failure to create a request object is fatal and likely
		// due to a bad URL. Return without retrying.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Errors from sending the request or HTTP error codes
		// should be retried, continue to the next loop iteration.
		rsp, err := http.DefaultClient.Do(req)
		switch {
		case err != nil:
			cancel(fmt.Errorf("GET request failed: %w", err))
			continue

		case rsp.StatusCode < 200 || rsp.StatusCode >= 300:
			cancel(fmt.Errorf("GET status error: %d/%s", rsp.StatusCode, rsp.Status))
			continue

		default:
			// Success!
			return nil
		}
	}

	return fmt.Errorf("GET failed: %w", r.Err())
}

func TestSuccess(t *testing.T) {
	ctx := testContext(t)
	err := ExampleRetry(ctx, "https://go.dev")
	t.Logf("received error: %v", err)

	if err != nil {
		t.Errorf("retry: %v", err)
	}
}

func TestFailure(t *testing.T) {
	ctx := testContext(t)
	err := ExampleRetry(ctx, "https://go.dev/404.html")
	t.Logf("received error: %v", err)

	if !errors.Is(err, retry.ErrRetriesExceeded) {
		t.Errorf("expected %v, got: %v", retry.ErrRetriesExceeded, err)
	}
}

func ExampleRecordErrors() {
	r := retry.Retry{Backoff: time.Millisecond}

	for _, cause := range r.Retry(context.Background()) {
		cause(errors.New("foobar"))
	}

	fmt.Print(r.Err())
	// Output:
	// retry count exceeded
	// foobar
	// foobar
	// foobar
	// foobar
}
