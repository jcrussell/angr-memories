package site

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/util"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Render renders s into outDir: one index.html for the homepage, one per
// flat prefix family, or an index + one per chunk for large families.
func Render(s *Site, outDir string) error {
	md := newGoldmark()

	for _, pi := range s.Prefixes {
		if pi.Chunked {
			for _, c := range pi.Chunks {
				renderGroups(md, c.Groups)
			}
		} else {
			renderGroups(md, pi.Groups)
		}
	}

	tpl, err := template.New("").Funcs(template.FuncMap{
		"url": func(from, target string) string {
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				return target
			}
			if s.BaseURL != "" {
				return s.BaseURL + target
			}
			return relativeURL(from, target)
		},
	}).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	if err := checkOutDirSafe(outDir); err != nil {
		return err
	}
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := writePage(tpl, "index.html", filepath.Join(outDir, "index.html"), pageData{
		Site: s, Path: "/", Title: "angr-memories",
	}); err != nil {
		return err
	}

	if err := writePage(tpl, "dashboard.html", filepath.Join(outDir, "dashboard", "index.html"), pageData{
		Site: s, Path: "/dashboard/", Title: "Dashboard — angr-memories",
	}); err != nil {
		return err
	}

	for _, pi := range s.Prefixes {
		if err := writePage(tpl, "prefix.html", filepath.Join(outDir, "prefix", pi.Name, "index.html"), pageData{
			Site: s, Prefix: pi, Path: pi.Path, Title: pi.Name + " — angr-memories",
		}); err != nil {
			return err
		}
		for _, c := range pi.Chunks {
			if err := writePage(tpl, "chunk.html", filepath.Join(outDir, "prefix", pi.Name, c.Label, "index.html"), pageData{
				Site: s, Prefix: pi, Chunk: c, Path: c.Path, Title: pi.Name + " " + c.Label + " — angr-memories",
			}); err != nil {
				return err
			}
		}
	}

	return copyStatic(outDir)
}

// checkOutDirSafe rejects the most obviously dangerous --out values before
// Render recursively deletes it. Not a security boundary (this is a local,
// single-operator CLI) -- just a footgun guard against a fat-fingered flag
// (`--out .`, `--out ..`, `--out /`) wiping something unintended.
func checkOutDirSafe(outDir string) error {
	clean := filepath.Clean(outDir)
	home, _ := os.UserHomeDir()
	switch {
	case clean == "", clean == ".", clean == "..", clean == "/":
		return fmt.Errorf("--out %q looks unintentional (resolves to %q); refusing to delete it", outDir, clean)
	case home != "" && clean == home:
		return fmt.Errorf("--out %q resolves to your home directory; refusing to delete it", outDir)
	}
	return nil
}

// relativeURL turns a root-relative target path into one relative to from
// (the current page's own root-relative directory), so the rendered site
// works when opened directly via file:// -- which, unlike an HTTP server,
// never resolves a bare directory reference to its index.html. A target
// that is empty or ends in "/" is treated as a directory and gets
// "index.html" appended before being made relative. Results aren't always
// the shortest possible path (e.g. a link between sibling pages may route
// via the site root), but they always resolve to a real file.
func relativeURL(from, target string) string {
	if target == "" || strings.HasSuffix(target, "/") {
		target += "index.html"
	}
	ups := strings.Repeat("../", len(pathSegments(from)))
	return ups + strings.TrimPrefix(target, "/")
}

func pathSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func renderGroups(md goldmark.Markdown, groups []*Group) {
	for _, g := range groups {
		for _, inc := range g.Incarnations {
			inc.BodyHTML = renderMarkdown(md, inc.Body)
		}
	}
}

// newGoldmark deliberately omits html.WithUnsafe(). Memory bodies come from
// a live external corpus (`bd memories --json`, pulled by `sync`) that
// nothing here curates or sanitizes -- with WithUnsafe(), a body containing
// literal HTML (accidental or otherwise, e.g. `<script>`) would render and
// execute in the generated static site. Without it, goldmark replaces raw
// HTML nodes with an inert `<!-- raw HTML omitted -->` comment instead.
//
// Residual cosmetic tradeoff: bodies were free-text bd notes, never
// authored with markdown escaping in mind, so a bare CommonMark-shaped
// placeholder like `<commit>` or `<addr>` (not inside a code span/fence)
// is still parsed as a raw-HTML tag and vanishes from the rendered page
// rather than displaying literally -- same as it would on GitHub or any
// other CommonMark renderer. The canonical data in memories.jsonl is
// unaffected either way; this only touches the rendered view, and the
// alternative (escaping `<`/`>` before handing text to goldmark) would
// double-escape genuine fenced code blocks, which is worse.
//
// For the same reason, the base inline-parser set below omits
// parser.NewLinkParser() (which handles both `[text](url)` links and
// `![alt](url)` images). Bodies are full of domain notation shaped like
// `Extract[h1:l1](x)` -- CommonMark happily parses that as a link with
// text "h1:l1" pointing at "x", silently swallowing the brackets/parens
// instead of displaying them. Dropping the paragraph transformer that
// collects `[label]: url` reference definitions too, for the same
// reason (those lines would otherwise vanish from the visible body).
// GFM's own bare-URL autolinking (extension.GFM's linkify) is a
// separate parser layered on below and is unaffected.
func newGoldmark() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithParser(parser.NewParser(
			parser.WithBlockParsers(parser.DefaultBlockParsers()...),
			parser.WithInlineParsers(
				util.Prioritized(parser.NewCodeSpanParser(), 100),
				util.Prioritized(parser.NewAutoLinkParser(), 300),
				util.Prioritized(parser.NewRawHTMLParser(), 400),
				util.Prioritized(parser.NewEmphasisParser(), 500),
			),
		)),
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
}

func renderMarkdown(md goldmark.Markdown, src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}

// chromaCSS pairs the "github" (light) and "github-dark" themes, each
// scoped to its own prefers-color-scheme media query.
func chromaCSS() ([]byte, error) {
	f := chromahtml.New()
	var buf bytes.Buffer
	buf.WriteString("/* generated from chroma styles: github + github-dark */\n")
	for _, t := range []struct{ scheme, style string }{
		{"light", "github"},
		{"dark", "github-dark"},
	} {
		var inner bytes.Buffer
		if err := f.WriteCSS(&inner, styles.Get(t.style)); err != nil {
			return nil, fmt.Errorf("write %s chroma CSS: %w", t.scheme, err)
		}
		fmt.Fprintf(&buf, "\n@media (prefers-color-scheme: %s) {\n  ", t.scheme)
		buf.WriteString(strings.ReplaceAll(strings.TrimRight(inner.String(), "\n"), "\n", "\n  "))
		buf.WriteString("\n}\n")
	}
	return buf.Bytes(), nil
}

type pageData struct {
	Site   *Site
	Prefix *PrefixIndex
	Chunk  *Chunk
	Path   string // this page's own root-relative directory, e.g. "/prefix/<name>/"
	Title  string
}

func writePage(tpl *template.Template, tmpl, outPath string, data pageData) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, tmpl, data); err != nil {
		return fmt.Errorf("execute %s: %w", tmpl, err)
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o644)
}

func copyStatic(outDir string) error {
	if err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(outDir, p) // p includes "static/" prefix
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	}); err != nil {
		return err
	}
	css, err := chromaCSS()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "static", "chroma.css"), css, 0o644)
}
