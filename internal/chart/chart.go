// Package chart renders small inline SVG charts as html/template.HTML
// fragments -- hand-rolled, no charting dependency, consistent with the
// project's minimal-deps posture (only chroma/goldmark otherwise). Inline
// SVG (embedded directly in the page, not a referenced .svg file) sees the
// page's own CSS, so fills specified as chart-* classes automatically pick
// up site.css's light/dark theming via prefers-color-scheme -- the same
// mechanism the Chroma syntax-highlighting CSS already uses.
package chart

const maxLabelLen = 48

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
