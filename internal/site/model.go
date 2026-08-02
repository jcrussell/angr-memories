// Package site walks memories.jsonl and renders it as a browsable static
// site under -out. Unlike byob-go-cli's site package (which this is
// modeled on), there's no epic/category tree here -- memories are grouped
// only by key-prefix family, and a single key can have more than one
// incarnation (forgotten, then recreated later).
package site

import (
	"html/template"
	"sort"

	"github.com/jcrussell/angr-memories/internal/memory"
)

// Site is the root data passed to templates.
type Site struct {
	Prefixes []*PrefixIndex // sorted by name, for the homepage

	TotalRemembered int
	TotalForgotten  int

	BaseURL string // e.g. "" for local preview
	RepoURL string
}

// PrefixIndex is one key-prefix family's summary + page(s).
type PrefixIndex struct {
	Name       string
	Path       string // "/prefix/<name>/"
	Remembered int
	Forgotten  int

	// Chunked is true when this family is too large for one flat page
	// (see memory.AlphaSplit) -- Chunks holds the sub-pages and Groups is
	// empty. When false, Groups holds every group directly and Chunks is
	// empty.
	Chunked bool
	Chunks  []*Chunk
	Groups  []*Group
}

// Chunk is one alpha-split sub-page of a large prefix family.
type Chunk struct {
	Label      string // "1", "2", ...
	Path       string // "/prefix/<name>/<label>/"
	Remembered int
	Forgotten  int
	Groups     []*Group
}

// Group is one key's full incarnation history, oldest first.
type Group struct {
	Key          string
	Incarnations []*Incarnation
}

// Recreated is true when this key was forgotten and later reappeared.
func (g *Group) Recreated() bool { return len(g.Incarnations) > 1 }

// Current is the most recent incarnation (what "remembered"/"forgotten"
// means for the group as a whole).
func (g *Group) Current() *Incarnation { return g.Incarnations[len(g.Incarnations)-1] }

// Incarnation is one Memory record plus its rendered HTML.
type Incarnation struct {
	*memory.Memory
	BodyHTML template.HTML
}

func sortGroups(gs []*Group) {
	sort.Slice(gs, func(i, j int) bool { return gs[i].Key < gs[j].Key })
}
