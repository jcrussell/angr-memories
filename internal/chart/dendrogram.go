package chart

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Geometry constants, in SVG user units; the viewBox scales the whole
// drawing to the container width. 24 px rows keep 48 labels at 13 px
// readable; the tree gets 300 px so merge distances stay distinguishable,
// labels 290 px (about 48 characters), bars 260 px, and the trailing 110 px
// holds the "N · P% kept" value. These match the prototype this page
// replaced, so the published tree looks like the one people reviewed.
const (
	dendroRow    = 24                      // height of one leaf row
	dendroTop    = 16                      // top padding above the first row
	dendroTreeW  = 300                     // width of the tree column
	dendroGap    = 10                      // gap between the tree and the labels
	dendroLabelX = dendroTreeW + dendroGap // x of the label column
	dendroBarX   = dendroLabelX + 290      // x of the bar column
	dendroBarW   = 260                     // width of the bar column at maxN
	dendroW      = dendroBarX + dendroBarW + 110
	dendroBarH   = 12 // height of the stacked bar
	dendroNodeR  = 9  // radius of the (transparent) hover target on each merge
	dendroRootX  = 8  // x of the root (maxH); leaves sit at x = dendroTreeW
	dendroBarGap = 2  // gap between the remembered and forgotten segments
)

// DendroNode is a truncated clustering tree: internal nodes carry the merge
// Height and Size; leaves have Left/Right nil and Row = index into the rows
// slice.
type DendroNode struct {
	Height      float64
	Size        int
	Left, Right *DendroNode
	Row         int
}

// DendroRow is one leaf of the tree: a labelled cluster with a stacked
// remembered/forgotten bar.
type DendroRow struct {
	Label      string
	Unlabeled  bool   // render label in the "unlabeled" style (italic, muted)
	Href       string // link target for the whole row (already resolved by the caller; may be empty = no link)
	Title      string // hover text for the row (<title>), may contain newlines
	Remembered float64
	Forgotten  float64
}

// dendroLeaf is a leaf in visit order: which DendroRow it shows and its
// pixel y (row centre).
type dendroLeaf struct {
	row DendroRow
	y   float64
}

// dendroMerge is one internal node laid out in pixels. xa/xb are the x of
// the children (x(child.Height)), ya/yb their y; x/y is the node itself.
type dendroMerge struct {
	x, y, xa, ya, xb, yb float64
	size                 int
	height               float64
}

// Dendrogram renders root+rows as one inline SVG: the clustering tree on the
// left (root at the far left, leaves at the right edge of the tree area),
// one labelled row per leaf, and a stacked remembered/forgotten bar per row.
// xAxisLabel captions the merge-distance axis (e.g. "Ward merge distance").
func Dendrogram(root *DendroNode, rows []DendroRow, xAxisLabel string) template.HTML {
	if root == nil || len(rows) == 0 {
		return template.HTML(`<p class="chart-empty">no data</p>`)
	}

	maxH := 0.0
	var scan func(n *DendroNode)
	scan = func(n *DendroNode) {
		if n == nil {
			return
		}
		if n.Left != nil || n.Right != nil {
			if n.Height > maxH {
				maxH = n.Height
			}
		}
		scan(n.Left)
		scan(n.Right)
	}
	scan(root)
	if maxH <= 0 {
		maxH = 1 // degenerate: every merge at distance 0
	}
	x := func(h float64) float64 { return dendroRootX + (dendroTreeW-dendroRootX)*(1-h/maxH) }

	maxN := 0.0
	for _, r := range rows {
		if t := r.Remembered + r.Forgotten; t > maxN {
			maxN = t
		}
	}
	if maxN <= 0 {
		maxN = 1
	}
	bx := func(v float64) float64 { return dendroBarW * v / maxN }

	// Lay out the tree: a recursive walk assigns leaves consecutive row
	// slots in visit order (left subtree first) and positions every internal
	// node at the midpoint of its children's y.
	var leaves []dendroLeaf
	var merges []dendroMerge
	y := func(i int) float64 { return dendroTop + dendroRow*float64(i) + dendroRow/2.0 }
	var walk func(n *DendroNode) (px, py float64)
	walk = func(n *DendroNode) (float64, float64) {
		switch {
		case n.Left == nil && n.Right == nil:
			var row DendroRow
			if n.Row >= 0 && n.Row < len(rows) {
				row = rows[n.Row]
			}
			py := y(len(leaves))
			leaves = append(leaves, dendroLeaf{row: row, y: py})
			return x(0), py
		case n.Left == nil:
			// Unary node (malformed input): pass the child straight through.
			return walk(n.Right)
		case n.Right == nil:
			return walk(n.Left)
		}
		xa, ya := walk(n.Left)
		xb, yb := walk(n.Right)
		px, py := x(n.Height), (ya+yb)/2
		merges = append(merges, dendroMerge{x: px, y: py, xa: xa, ya: ya, xb: xb, yb: yb, size: n.Size, height: n.Height})
		return px, py
	}
	walk(root)

	nRows := len(leaves)
	height := dendroTop + dendroRow*nRows + 40
	rowsBottom := float64(dendroTop + dendroRow*nRows)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="dendro" viewBox="0 0 %d %d" role="img" aria-label="topic dendrogram">`, dendroW, height)

	// Merge-distance gridlines and axis.
	for k := 0; k < 5; k++ {
		t := maxH * float64(k) / 4
		gx := x(t)
		fmt.Fprintf(&b, `<line class="dendro-grid" x1="%.1f" x2="%.1f" y1="%d" y2="%.1f"/>`, gx, gx, dendroTop-6, rowsBottom)
		fmt.Fprintf(&b, `<text class="dendro-axis" x="%.1f" y="%.1f" text-anchor="middle">%.1f</text>`, gx, rowsBottom+16, t)
	}
	fmt.Fprintf(&b, `<text class="dendro-axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`,
		dendroTreeW/2.0, rowsBottom+32, template.HTMLEscapeString(xAxisLabel))

	// Memory-count gridlines and axis under the bar column.
	for _, t := range []float64{0, 50, 100, 150} {
		if t > maxN {
			continue
		}
		gx := dendroBarX + bx(t)
		fmt.Fprintf(&b, `<line class="dendro-grid" x1="%.1f" x2="%.1f" y1="%d" y2="%.1f"/>`, gx, gx, dendroTop-6, rowsBottom)
		fmt.Fprintf(&b, `<text class="dendro-axis" x="%.1f" y="%.1f" text-anchor="middle">%.0f</text>`, gx, rowsBottom+16, t)
	}
	fmt.Fprintf(&b, `<text class="dendro-axis" x="%.1f" y="%.1f" text-anchor="middle">memories</text>`,
		dendroBarX+dendroBarW/2.0, rowsBottom+32)

	// Tree: one elbow path per merge plus a transparent hover target.
	for _, m := range merges {
		fmt.Fprintf(&b, `<path class="dendro-link" d="M%.1f,%.1fH%.1fV%.1fH%.1f"/>`, m.xa, m.ya, m.x, m.yb, m.xb)
		fmt.Fprintf(&b, `<circle class="dendro-node" cx="%.1f" cy="%.1f" r="%d"><title>Merges %d memories at distance %.2f</title></circle>`,
			m.x, m.y, dendroNodeR, m.size, m.height)
	}

	// Rows: label, stacked bar, value text; the whole row is a hover/link target.
	for _, lf := range leaves {
		r := lf.row
		py := lf.y

		label := truncate(r.Label, maxLabelLen)
		title := r.Title
		if label != r.Label {
			if title == "" {
				title = r.Label
			} else {
				title = r.Label + "\n" + title
			}
		}
		if title == "" {
			title = r.Label
		}

		if r.Href != "" {
			fmt.Fprintf(&b, `<a href="%s">`, template.HTMLEscapeString(r.Href))
		}
		b.WriteString(`<g class="dendro-row">`)
		fmt.Fprintf(&b, `<title>%s</title>`, template.HTMLEscapeString(title))
		fmt.Fprintf(&b, `<rect class="dendro-hit" x="%d" y="%.1f" width="%d" height="%d"/>`,
			dendroTreeW, py-dendroRow/2.0, dendroW-dendroTreeW, dendroRow)
		labelClass := "dendro-label"
		if r.Unlabeled {
			labelClass += " unlabeled"
		}
		fmt.Fprintf(&b, `<text class="%s" x="%d" y="%.1f">%s</text>`, labelClass, dendroLabelX, py+4, template.HTMLEscapeString(label))

		w1, w2 := bx(r.Remembered), bx(r.Forgotten)
		barY := py - dendroBarH/2.0
		if r.Remembered > 0 {
			rx := 4
			if r.Forgotten > 0 {
				rx = 2
			}
			fmt.Fprintf(&b, `<rect class="chart-bar-remembered" x="%.1f" y="%.1f" width="%.1f" height="%d" rx="%d"/>`,
				float64(dendroBarX), barY, w1, dendroBarH, rx)
		}
		if r.Forgotten > 0 {
			fx, fw := float64(dendroBarX)+w1, w2
			if r.Remembered > 0 {
				fx += dendroBarGap
				fw = math.Max(w2-dendroBarGap, 1)
			}
			fmt.Fprintf(&b, `<rect class="chart-bar-forgotten" x="%.1f" y="%.1f" width="%.1f" height="%d" rx="4"/>`,
				fx, barY, fw, dendroBarH)
		}

		n := int(math.Round(r.Remembered + r.Forgotten))
		pct := 0 // same rounding as topics.KeptPercent; chart must not import topics
		if n > 0 {
			pct = int(math.Round(100 * r.Remembered / float64(n)))
		}
		fmt.Fprintf(&b, `<text class="dendro-value" x="%.1f" y="%.1f">%d · %d%% kept</text>`,
			float64(dendroBarX)+w1+w2+6, py+4, n, pct)

		b.WriteString(`</g>`)
		if r.Href != "" {
			b.WriteString(`</a>`)
		}
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
