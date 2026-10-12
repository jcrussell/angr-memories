package cmdutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(next http.RoundTripper) (*http.Client, *[]time.Duration) {
	var slept []time.Duration
	rt := &retryTransport{next: &userAgentTransport{next: next}, maxAttempts: 3, baseDelay: time.Millisecond,
		sleep: func(_ context.Context, d time.Duration) { slept = append(slept, d) }}
	return &http.Client{Transport: rt}, &slept
}

func TestHTTPClient_SetsUserAgentAndRetries5xxWithBodyReplay(t *testing.T) {
	var calls atomic.Int32
	var seenUA, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		seenUA, seenBody = r.Header.Get("User-Agent"), string(b)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, slept := newTestClient(http.DefaultTransport)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/api/embed", strings.NewReader(`{"x":1}`))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || calls.Load() != 3 {
		t.Errorf("status=%d calls=%d, want 200 after 3 attempts", resp.StatusCode, calls.Load())
	}
	if !strings.HasPrefix(seenUA, "angr-memories/") {
		t.Errorf("User-Agent = %q", seenUA)
	}
	if seenBody != `{"x":1}` {
		t.Errorf("body not replayed on retry: %q", seenBody)
	}
	if len(*slept) != 2 {
		t.Errorf("expected 2 sleeps between 3 attempts, got %v", *slept)
	}
}

func TestHTTPClient_ReturnsLastResponseAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":"model crashed"}`)
	}))
	defer srv.Close()
	c, slept := newTestClient(http.DefaultTransport)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("the final 5xx must be returned, not turned into an error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 || string(body) != `{"error":"model crashed"}` || calls.Load() != 3 || len(*slept) != 2 {
		t.Errorf("status=%d body=%q calls=%d sleeps=%d", resp.StatusCode, body, calls.Load(), len(*slept))
	}
}

func TestHTTPClient_DoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := newTestClient(http.DefaultTransport)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 || calls.Load() != 1 {
		t.Errorf("status=%d calls=%d, want a single 404", resp.StatusCode, calls.Load())
	}
}

func TestHTTPClient_RetriesConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens any more
	c, slept := newTestClient(http.DefaultTransport)
	_, err := c.Get(url)
	if err == nil {
		t.Fatal("expected an error against a closed server")
	}
	if len(*slept) != 2 {
		t.Errorf("expected 2 retries for connection refused, got sleeps %v", *slept)
	}
}

func TestHTTPClient_DoesNotRetry500AndCapsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/slow" {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, slept := newTestClient(http.DefaultTransport)
	resp, err := c.Get(srv.URL + "/boom")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 500 || calls.Load() != 1 {
		t.Errorf("a 500 must come straight back: status=%d calls=%d", resp.StatusCode, calls.Load())
	}
	resp, err = c.Get(srv.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, d := range *slept {
		if d > maxRetryDelay {
			t.Errorf("Retry-After not capped: slept %v", d)
		}
	}
	if len(*slept) != 2 {
		t.Errorf("expected 2 capped sleeps, got %v", *slept)
	}
}

func TestHTTPClient_SleepStopsOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	rt := &retryTransport{next: http.DefaultTransport, maxAttempts: 3, baseDelay: time.Millisecond}
	c := &http.Client{Transport: rt}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	start := time.Now()
	_, err := c.Do(req)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel should end the retry sleep promptly: err=%v after %v", err, time.Since(start))
	}
}

// errReader is a body with no GetBody: it can be sent once and never replayed.
type onceBody struct{ r io.Reader }

func (o onceBody) Read(p []byte) (int, error) { return o.r.Read(p) }
func (o onceBody) Close() error               { return nil }

func TestHTTPClient_NonReplayableBodyGetsOneAttemptAndTheRealResponse(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "busy")
	}))
	defer srv.Close()
	c, _ := newTestClient(http.DefaultTransport)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	req.Body = onceBody{strings.NewReader("stream")}
	req.GetBody = nil
	req.ContentLength = -1
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("must not fail with a nil response/nil error pair: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 || string(body) != "busy" || calls.Load() != 1 {
		t.Errorf("status=%d body=%q calls=%d; want the single real 503", resp.StatusCode, body, calls.Load())
	}
}
