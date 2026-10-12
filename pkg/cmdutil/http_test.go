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
		sleep: func(d time.Duration) { slept = append(slept, d) }}
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

func TestHTTPClient_GivesUpAfterMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := newTestClient(http.DefaultTransport)
	_, err := c.Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("expected give-up error, got %v", err)
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
