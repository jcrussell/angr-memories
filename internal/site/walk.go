package site

import (
	"sort"
	"strconv"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/topics"
)

// Options tunes the optional topics section. Zero-value Options (Walk's
// default) produces a site without topics.
type Options struct {
	TopicK  int                  // clusters to cut the tree at; 0 = topics.DefaultK
	Labels  []topics.Label       // human/LLM labels, anchored to member keys
	Vectors map[string][]float32 // key -> unit embedding; keys without one are left out of clustering
	Model   string               // embedding model name, shown on the topics page
}

// defaultChunkCap uses memory.AlphaSplit (the same alpha-split algorithm as
// bd_chunk_for_review.py) but with a larger cap than that tool's --chunk-cap
// 70 default: 70 was calibrated for agent-review cognitive load, not
// webpage size. A flat page comfortably holds a few hundred entries (avoid
// at 167 and benchmark at 141 combined entries render fine as one page,
// each well under 200KB), so a much higher cap is used here -- just high
// enough that only "catch" (~1,120 combined entries, confirmed ~900KB-1MB
// as a single page) and "invariant" (~490, growing via sync) actually
// split, matching every other family staying flat.
const defaultChunkCap = 300

// Walk reads memoriesPath and returns a Site with every key-prefix family
// grouped, ordered, and (where large) chunked. HTML rendering happens in a
// separate Render pass so Walk stays purely IO+grouping.
func Walk(memoriesPath string) (*Site, error) { return WalkWith(memoriesPath, Options{}) }

// WalkWith is Walk plus the topics section when opts carries vectors.
func WalkWith(memoriesPath string, opts Options) (*Site, error) {
	records, err := memory.Load(memoriesPath)
	if err != nil {
		return nil, err
	}

	byKey := map[string][]*memory.Memory{}
	for _, m := range records {
		byKey[m.Key] = append(byKey[m.Key], m)
	}

	byPrefix := map[string][]*Group{}
	s := &Site{}
	for key, incarnations := range byKey {
		sort.Slice(incarnations, func(i, j int) bool {
			return incarnations[i].FirstSeen < incarnations[j].FirstSeen
		})
		g := &Group{Key: key}
		for _, m := range incarnations {
			g.Incarnations = append(g.Incarnations, &Incarnation{Memory: m})
		}
		prefix := memory.PrefixOf(key)
		byPrefix[prefix] = append(byPrefix[prefix], g)

		if g.Current().Remembered() {
			s.TotalRemembered++
		} else {
			s.TotalForgotten++
		}
	}

	prefixNames := make([]string, 0, len(byPrefix))
	for name := range byPrefix {
		prefixNames = append(prefixNames, name)
	}
	sort.Strings(prefixNames)

	keyPage := make(map[string]string, len(byKey)) // key -> page holding its anchor
	for _, name := range prefixNames {
		groups := byPrefix[name]
		sortGroups(groups)

		pi := &PrefixIndex{Name: name, Path: "/prefix/" + name + "/"}
		for _, g := range groups {
			if g.Current().Remembered() {
				pi.Remembered++
			} else {
				pi.Forgotten++
			}
		}

		keys := make([]string, len(groups))
		for i, g := range groups {
			keys[i] = g.Key
		}
		chunks := memory.AlphaSplit(keys, defaultChunkCap)

		if len(chunks) == 1 {
			pi.Groups = groups
			for _, g := range groups {
				keyPage[g.Key] = pi.Path
			}
		} else {
			pi.Chunked = true
			byGroupKey := make(map[string]*Group, len(groups))
			for _, g := range groups {
				byGroupKey[g.Key] = g
			}
			for i, chunkKeys := range chunks {
				label := indexLabel(i)
				c := &Chunk{Label: label, Path: pi.Path + label + "/"}
				for _, k := range chunkKeys {
					g := byGroupKey[k]
					keyPage[k] = c.Path
					c.Groups = append(c.Groups, g)
					if g.Current().Remembered() {
						c.Remembered++
					} else {
						c.Forgotten++
					}
				}
				pi.Chunks = append(pi.Chunks, c)
			}
		}

		s.Prefixes = append(s.Prefixes, pi)
	}

	s.Dashboard = buildDashboard(records)
	if len(opts.Vectors) > 0 {
		s.Topics = buildTopics(records, keyPage, opts)
	}

	return s, nil
}

// indexLabel is 1-indexed, matching bd_chunk_for_review.py's batch-N
// convention.
func indexLabel(i int) string { return strconv.Itoa(i + 1) }
