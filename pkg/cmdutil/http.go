package cmdutil

import (
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
		Transport: &retryTransport{next: &userAgentTransport{next: base}, maxAttempts: 3, baseDelay: 200 * time.Millisecond, sleep: time.Sleep},
	}
}

type userAgentTransport struct{ next http.RoundTripper }

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", UserAgent)
	return t.next.RoundTrip(req)
}

// retryTransport retries connection failures and 5xx/429 responses with
// bounded exponential backoff plus jitter, honoring Retry-After. Only
// requests whose body can be replayed (GetBody set, which http.NewRequest
// does for bytes/strings readers) or that have no body are retried; the
// embedding and chat calls are stateless on the server, so replaying a
// POST is safe here.
type retryTransport struct {
	next        http.RoundTripper
	maxAttempts int
	baseDelay   time.Duration
	sleep       func(time.Duration)
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		r := req
		if attempt > 1 && req.Body != nil && req.Body != http.NoBody {
			if req.GetBody == nil {
				return nil, lastErr
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r = req.Clone(req.Context())
			r.Body = body
		}
		resp, err := t.next.RoundTrip(r)
		retryable, delay := t.shouldRetry(resp, err, attempt)
		if !retryable || (resp != nil && attempt >= t.maxAttempts) {
			return resp, err
		}
		if resp != nil {
			resp.Body.Close()
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("%s %s: server returned %s", req.Method, req.URL.Path, resp.Status)
		}
		if attempt >= t.maxAttempts {
			if resp != nil {
				// Hand the final response back as-is so the caller sees the
				// real status and error body instead of a synthetic error.
				return resp, nil
			}
			return nil, fmt.Errorf("after %d attempts: %w", attempt, lastErr)
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		default:
		}
		t.sleep(delay)
	}
}

func (t *retryTransport) shouldRetry(resp *http.Response, err error, attempt int) (bool, time.Duration) {
	backoff := t.baseDelay * time.Duration(1<<(attempt-1))
	backoff += time.Duration(rand.Int64N(int64(t.baseDelay))) // jitter
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, net.ErrClosed) {
			return true, backoff
		}
		var opErr *net.OpError
		return errors.As(err, &opErr), backoff
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, perr := strconv.Atoi(ra); perr == nil && secs >= 0 {
				return true, time.Duration(secs) * time.Second
			}
		}
		return true, backoff
	}
	return false, 0
}
