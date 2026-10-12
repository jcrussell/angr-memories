package ollamaclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

func TestEmbedder_SendsRequestAndNormalizes(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","embeddings":[[3,4],[0,2]]}`)
	}))
	defer srv.Close()
	c, err := New(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	e := &Embedder{Client: c, Model: "m", Dims: 2, Endpoint: srv.URL}
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != "m" || got["truncate"] != false || got["dimensions"] != float64(2) {
		t.Errorf("request = %v", got)
	}
	if math.Abs(float64(vecs[0][0])-0.6) > 1e-6 || math.Abs(float64(vecs[1][1])-1) > 1e-6 {
		t.Errorf("not normalized: %v", vecs)
	}
}

func TestEmbedder_DimMismatchIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"m","embeddings":[[1,0],[1,0,0]]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Embedder{Client: c, Model: "m"}).Embed(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "dim") {
		t.Fatalf("got %v, want a dims error", err)
	}
}

func TestEmbedder_ModelNotFoundHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"model 'nope' not found"}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Embedder{Client: c, Model: "nope", Endpoint: srv.URL}).Embed(context.Background(), []string{"a"})
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(h.Hint, "ollama pull nope") {
		t.Fatalf("got %v, want an ErrHint pointing at ollama pull", err)
	}
}

func TestHeartbeat_ConnectionRefusedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c, _ := New(url, &http.Client{})
	err := Heartbeat(context.Background(), c, url)
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(h.Hint, "ollama serve") {
		t.Fatalf("got %v, want an ErrHint pointing at ollama serve", err)
	}
}

func TestLabeler_StructuredOutput(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		io.WriteString(w, `{"model":"c","message":{"role":"assistant","content":"{\"label\": \"Z3 solver context discipline \"}"},"done":true}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	l := &Labeler{Client: c, Model: "c", Endpoint: srv.URL}
	label, err := l.Suggest(context.Background(), ClusterBrief{Size: 3, Keys: []string{"z3-a", "z3-b"}, Excerpts: []string{"z3-a: ctx"}, Examples: []string{"Paged memory & permissions"}, Avoid: []string{"Solver"}})
	if err != nil {
		t.Fatal(err)
	}
	if label != "Z3 solver context discipline" {
		t.Errorf("label = %q", label)
	}
	if got["stream"] != false || got["format"] == nil || got["model"] != "c" {
		t.Errorf("request = %v", got)
	}
	msgs := got["messages"].([]any)
	user := msgs[1].(map[string]any)["content"].(string)
	for _, want := range []string{"z3-a", "Paged memory", "already used", "Solver"} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt missing %q:\n%s", want, user)
		}
	}
}

func TestLabeler_NonJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"c","message":{"role":"assistant","content":"Solver stuff"},"done":true}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Labeler{Client: c, Model: "c"}).Suggest(context.Background(), ClusterBrief{})
	if err == nil || !strings.Contains(err.Error(), "non-JSON") {
		t.Fatalf("got %v", err)
	}
}

func TestEndpointParsing(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	if EndpointFromEnv() != DefaultEndpoint {
		t.Errorf("default = %q", EndpointFromEnv())
	}
	t.Setenv("OLLAMA_HOST", "gpu-box:11434")
	if EndpointFromEnv() != "http://gpu-box:11434" {
		t.Errorf("bare host = %q", EndpointFromEnv())
	}
	for _, bad := range []string{"", "11434", "ftp://x", "localhost:11434"} {
		if _, err := ParseEndpoint(bad); err == nil {
			t.Errorf("ParseEndpoint(%q) should fail", bad)
		}
	}
}
