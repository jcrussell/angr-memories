package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// UserAgent identifies this tool to HTTP servers (byob-http-client.5).
// There is no build-info package yet, so the version is a constant.
var UserAgent = fmt.Sprintf("angr-memories/dev (%s; %s)", runtime.GOOS, runtime.GOARCH)

// NewHTTPClient returns the client every network-bound command shares:
// stdlib net/http with explicit timeouts and a RoundTripper chain that
// adds the User-Agent and a bounded retry (byob-http-client.1/.2/.3). The
// overall per-request timeout is generous because a local model server can
// take tens of seconds to load a model on first use.
func NewHTTPClient() *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.ResponseHeaderTimeout = 5 * time.Minute
	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: &retryTransport{next: &userAgentTransport{next: base}, maxAttempts: 3, baseDelay: 200 * time.Millisecond},
	}
}

type userAgentTransport struct{ next http.RoundTripper }

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", UserAgent)
	return t.next.RoundTrip(req)
}

// retryTransport retries connection failures and transient statuses (429,
// 502, 503, 504) with bounded exponential backoff plus jitter, honoring a
// capped Retry-After. A 500 is not retried: it usually means the request
// itself failed (runner crash, bad input) and replaying a multi-minute
// generation would only delay the message. Only requests whose body can be
// replayed (GetBody set, which http.NewRequest does for bytes/strings
// readers) or that have no body are retried; the embedding and chat calls
// are stateless on the server, so replaying a POST is safe here. Sleeps
// end early when the request's context is cancelled.
type retryTransport struct {
	next        http.RoundTripper
	maxAttempts int
	baseDelay   time.Duration
	sleep       func(context.Context, time.Duration) // nil = sleepCtx
}

const maxRetryDelay = 30 * time.Second

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A body that cannot be replayed means a single attempt: the first
	// response (or error) is final.
	replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	var lastErr error
	for attempt := 1; ; attempt++ {
		r := req
		if attempt > 1 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r = req.Clone(req.Context())
			r.Body = body
		}
		resp, err := t.next.RoundTrip(r)
		retryable, delay := t.shouldRetry(resp, err, attempt)
		// The final response is handed back as-is so the caller sees the
		// real status and error body instead of a synthetic error.
		if !retryable || !replayable || (resp != nil && attempt >= t.maxAttempts) {
			return resp, err
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = err
		if attempt >= t.maxAttempts {
			return nil, fmt.Errorf("after %d attempts: %w", attempt, lastErr)
		}
		if t.sleep != nil {
			t.sleep(req.Context(), delay)
		} else {
			sleepCtx(req.Context(), delay)
		}
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
	}
}

func (t *retryTransport) shouldRetry(resp *http.Response, err error, attempt int) (bool, time.Duration) {
	backoff := t.baseDelay * time.Duration(1<<(attempt-1))
	backoff += time.Duration(rand.Int64N(int64(t.baseDelay))) // jitter
	if err != nil {
		var ne net.Error
		return errors.As(err, &ne) || errors.Is(err, net.ErrClosed), backoff
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, perr := strconv.Atoi(ra); perr == nil && secs >= 0 {
				return true, min(time.Duration(secs)*time.Second, maxRetryDelay)
			}
		}
		return true, backoff
	}
	return false, 0
}
