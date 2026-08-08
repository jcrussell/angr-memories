package chart

import (
	"fmt"
	"html/template"
	"strings"
)

const (
	barRowHeight  = 22
	barRowGap     = 6
	barLabelWidth = 300
	barPlotWidth  = 340
	barValueGap   = 10
)

// Segment is one colored piece of a (possibly stacked) horizontal bar.
// Class selects the fill via a "chart-bar-<class>" CSS class; an empty
// Class falls back to "chart-bar-default".
type Segment struct {
	Value float64
	Class string
}

// Row is one labeled, possibly-stacked horizontal bar.
type Row struct {
	Label    string
	Segments []Segment
}

// HorizontalBars renders rows as a stacked horizontal bar chart, scaled so
// the longest row's total spans the chart's plot width. valueFmt formats
// each row's total for the trailing value label.
func HorizontalBars(rows []Row, valueFmt func(total float64) string) template.HTML {
	if len(rows) == 0 {
		return template.HTML(`<p class="chart-empty">no data</p>`)
	}

	maxTotal := 0.0
	for _, r := range rows {
		total := 0.0
		for _, s := range r.Segments {
			total += s.Value
		}
		if total > maxTotal {
			maxTotal = total
		}
	}
	if maxTotal == 0 {
		maxTotal = 1
	}

	height := len(rows)*(barRowHeight+barRowGap) - barRowGap
	width := barLabelWidth + barPlotWidth + barValueGap + 60

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" role="img" aria-label="bar chart">`, width, height)

	for i, r := range rows {
		y := i * (barRowHeight + barRowGap)
		textY := y + barRowHeight/2 + 4

		fmt.Fprintf(&b, `<text x="0" y="%d" class="chart-label">%s<title>%s</title></text>`,
			textY, template.HTMLEscapeString(truncate(r.Label, maxLabelLen)), template.HTMLEscapeString(r.Label))

		x := float64(barLabelWidth)
		total := 0.0
		for _, seg := range r.Segments {
			total += seg.Value
			w := seg.Value / maxTotal * barPlotWidth
			class := "chart-bar-default"
			if seg.Class != "" {
				class = "chart-bar-" + seg.Class
			}
			if w > 0 {
				fmt.Fprintf(&b, `<rect x="%.2f" y="%d" width="%.2f" height="%d" class="%s"/>`,
					x, y, w, barRowHeight, class)
			}
			x += w
		}

		fmt.Fprintf(&b, `<text x="%.2f" y="%d" class="chart-value">%s</text>`,
			float64(barLabelWidth+barPlotWidth)+barValueGap, textY,
			template.HTMLEscapeString(valueFmt(total)))
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
