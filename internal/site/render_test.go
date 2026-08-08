package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
)

func TestRenderMarkdown_DoesNotTreatBracketNotationAsLinks(t *testing.T) {
	md := newGoldmark()
	got := string(renderMarkdown(md, "Concat(Extract[h1:l1](x), Extract[h2:l2](x))"))
	if strings.Contains(got, "<a ") {
		t.Errorf("expected Extract[h1:l1](x)-shaped domain notation not to be parsed as a link, got: %s", got)
	}
	if !strings.Contains(got, "Extract[h1:l1](x)") {
		t.Errorf("expected the bracket/paren notation to render literally, got: %s", got)
	}
}

func TestRenderMarkdown_StillSupportsBasicEmphasisAndCode(t *testing.T) {
	md := newGoldmark()
	got := string(renderMarkdown(md, "**bold** and `code`"))
	if !strings.Contains(got, "<strong>bold</strong>") {
		t.Errorf("expected bold emphasis to still render, got: %s", got)
	}
	if !strings.Contains(got, "<code>code</code>") {
		t.Errorf("expected inline code to still render, got: %s", got)
	}
}

func TestRelativeURL(t *testing.T) {
	cases := []struct {
		from, target, want string
	}{
		{"/", "/", "index.html"},
		{"/", "/static/site.css", "static/site.css"},
		{"/", "/dashboard/", "dashboard/index.html"},
		{"/dashboard/", "/", "../index.html"},
		{"/dashboard/", "/static/site.css", "../static/site.css"},
		{"/prefix/x/", "/static/site.css", "../../static/site.css"},
		{"/prefix/x/", "/prefix/x/1/", "../../prefix/x/1/index.html"},
		{"/prefix/x/1/", "/prefix/x/", "../../../prefix/x/index.html"},
		{"/prefix/x/1/", "/", "../../../index.html"},
	}
	for _, c := range cases {
		if got := relativeURL(c.from, c.target); got != c.want {
			t.Errorf("relativeURL(%q, %q) = %q, want %q", c.from, c.target, got, c.want)
		}
	}
}

func TestRender_EmitsRelativeLinksByDefault(t *testing.T) {
	memPath := writeMemoriesFixture(t, []*memory.Memory{
		{Key: "invariant-a", Body: "body", FirstSeen: "2026-06-04T00:00:00Z"},
	})
	s, err := Walk(memPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "_site")
	if err := Render(s, out); err != nil {
		t.Fatal(err)
	}

	index, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), `href="/`) {
		t.Errorf("expected no root-absolute hrefs in index.html, got: %s", index)
	}
	if !strings.Contains(string(index), `href="static/site.css"`) {
		t.Errorf("expected a page-relative stylesheet link in index.html, got: %s", index)
	}
	if !strings.Contains(string(index), `href="prefix/invariant/index.html"`) {
		t.Errorf("expected a page-relative prefix link in index.html, got: %s", index)
	}

	dashboard, err := os.ReadFile(filepath.Join(out, "dashboard", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dashboard), `href="../static/site.css"`) {
		t.Errorf("expected the dashboard page's stylesheet link to climb back to the site root, got: %s", dashboard)
	}
}

func TestRender_BaseURLKeepsAbsoluteLinks(t *testing.T) {
	memPath := writeMemoriesFixture(t, []*memory.Memory{
		{Key: "invariant-a", Body: "body", FirstSeen: "2026-06-04T00:00:00Z"},
	})
	s, err := Walk(memPath)
	if err != nil {
		t.Fatal(err)
	}
	s.BaseURL = "/x"
	out := filepath.Join(t.TempDir(), "_site")
	if err := Render(s, out); err != nil {
		t.Fatal(err)
	}

	index, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `href="/x/static/site.css"`) {
		t.Errorf("expected --base-url to keep prefixed absolute links, got: %s", index)
	}
}
