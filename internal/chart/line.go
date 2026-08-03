package chart

import (
	"fmt"
	"html/template"
	"strings"
)

const (
	lineWidth  = 640
	lineHeight = 200
	linePadL   = 44
	linePadB   = 24
	linePadT   = 12
)

// Point is one (label, value) sample for LineChart. Label is shown on the
// X axis for a subset of points (see labelEvery) -- a dense daily time
// series would otherwise collide if every point were labeled.
type Point struct {
	Label string
	Value float64
}

// LineChart renders points as a single polyline over a scaled Y axis.
// valueFmt formats the min/max Y-axis labels.
func LineChart(points []Point, valueFmt func(float64) string) template.HTML {
	if len(points) < 2 {
		return template.HTML(`<p class="chart-empty">not enough data yet</p>`)
	}

	minV, maxV := points[0].Value, points[0].Value
	for _, p := range points {
		if p.Value < minV {
			minV = p.Value
		}
		if p.Value > maxV {
			maxV = p.Value
		}
	}
	if maxV == minV {
		maxV = minV + 1
	}

	plotW := float64(lineWidth - linePadL)
	plotH := float64(lineHeight - linePadT - linePadB)
	xAt := func(i int) float64 { return float64(linePadL) + plotW*float64(i)/float64(len(points)-1) }
	yAt := func(v float64) float64 { return float64(linePadT) + plotH - (v-minV)/(maxV-minV)*plotH }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart chart-line-svg" viewBox="0 0 %d %d" role="img" aria-label="line chart">`,
		lineWidth, lineHeight)

	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="chart-axis"/>`,
		linePadL, lineHeight-linePadB, lineWidth, lineHeight-linePadB)

	b.WriteString(`<polyline points="`)
	for i, p := range points {
		fmt.Fprintf(&b, "%.2f,%.2f ", xAt(i), yAt(p.Value))
	}
	b.WriteString(`" class="chart-line"/>`)

	labelEvery := len(points)/8 + 1
	for i, p := range points {
		if i%labelEvery != 0 && i != len(points)-1 {
			continue
		}
		fmt.Fprintf(&b, `<text x="%.2f" y="%d" class="chart-axis-label chart-axis-label-x">%s</text>`,
			xAt(i), lineHeight-4, template.HTMLEscapeString(p.Label))
	}

	fmt.Fprintf(&b, `<text x="2" y="%.2f" class="chart-axis-label">%s</text>`,
		yAt(maxV)+4, template.HTMLEscapeString(valueFmt(maxV)))
	fmt.Fprintf(&b, `<text x="2" y="%.2f" class="chart-axis-label">%s</text>`,
		yAt(minV)+4, template.HTMLEscapeString(valueFmt(minV)))

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
