package snapshot

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"runtime/pprof"
	"sort"
	"strings"
	"time"
)

type Request struct {
	GroupBy       string   `json:"groupBy"`
	TopN          int      `json:"topN"`
	MaxGoroutines int      `json:"maxGoroutines"`
	Include       []string `json:"include"`
	Exclude       []string `json:"exclude"`
	WithDelta     bool     `json:"withDelta"`
}

type Item struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Count          int            `json:"count"`
	StateTop       string         `json:"stateTop"`
	StateHistogram map[string]int `json:"stateHistogram,omitempty"`
	SampleFrames   []string       `json:"sampleFrames,omitempty"`
	TopFrames      []string       `json:"topFrames,omitempty"`
	Stack          []string       `json:"stack,omitempty"`
	RawSample      string         `json:"rawSample,omitempty"`
}

type DeltaEntry struct {
	ID    string `json:"id"`
	Delta int    `json:"delta"`
}

type Snapshot struct {
	SnapshotID              string `json:"snapshotId"`
	SnapshotTS              int64  `json:"snapshotTs"`
	GroupBy                 string `json:"groupBy"`
	TotalGoroutinesObserved int    `json:"totalObserved"`
	Items                   []Item `json:"items"`
	Delta                   struct {
		BaseSnapshotID string       `json:"baseSnapshotId,omitempty"`
		GrowthTop      []DeltaEntry `json:"growthTop,omitempty"`
	} `json:"delta"`
}

type block struct {
	state string
	stack []string
	raw   string
}

func Capture(req Request, previous *Snapshot) (*Snapshot, error) {
	if req.GroupBy == "" {
		req.GroupBy = "topFunction"
	}
	if req.TopN <= 0 {
		req.TopN = 20
	}
	if req.MaxGoroutines <= 0 {
		req.MaxGoroutines = 200000
	}
	if len(req.Exclude) == 0 {
		req.Exclude = []string{"runtime.", "net/http"}
	}

	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 2); err != nil {
		return nil, err
	}

	blocks := parse(buf.String(), req.MaxGoroutines, req.Include, req.Exclude)
	groups := map[string]*Item{}
	for _, b := range blocks {
		key, name := groupKey(req.GroupBy, b)
		if key == "" {
			continue
		}
		item, ok := groups[key]
		if !ok {
			item = &Item{
				ID:             shortHash(key),
				Name:           name,
				StateHistogram: map[string]int{},
				SampleFrames:   takeFrames(b.stack, 3),
				TopFrames:      takeFrames(b.stack, 3),
				Stack:          append([]string(nil), b.stack...),
				RawSample:      b.raw,
			}
			groups[key] = item
		}
		item.Count++
		item.StateHistogram[b.state]++
		if item.StateTop == "" || item.StateHistogram[b.state] > item.StateHistogram[item.StateTop] {
			item.StateTop = b.state
		}
	}

	items := make([]Item, 0, len(groups))
	for _, item := range groups {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Count > items[j].Count
	})
	if len(items) > req.TopN {
		items = items[:req.TopN]
	}

	now := time.Now().Unix()
	out := &Snapshot{
		SnapshotID:              "snp_" + shortHash(string(nowString(now))),
		SnapshotTS:              now,
		GroupBy:                 req.GroupBy,
		TotalGoroutinesObserved: len(blocks),
		Items:                   items,
	}

	if previous != nil && req.WithDelta {
		prevCounts := map[string]int{}
		for _, item := range previous.Items {
			prevCounts[item.ID] = item.Count
		}
		deltas := make([]DeltaEntry, 0, len(items))
		for _, item := range items {
			deltas = append(deltas, DeltaEntry{
				ID:    item.ID,
				Delta: item.Count - prevCounts[item.ID],
			})
		}
		sort.Slice(deltas, func(i, j int) bool {
			return deltas[i].Delta > deltas[j].Delta
		})
		if len(deltas) > 5 {
			deltas = deltas[:5]
		}
		out.Delta.BaseSnapshotID = previous.SnapshotID
		out.Delta.GrowthTop = deltas
	}
	return out, nil
}

func FindItem(s *Snapshot, id string) *Item {
	if s == nil {
		return nil
	}
	for i := range s.Items {
		if s.Items[i].ID == id {
			return &s.Items[i]
		}
	}
	return nil
}

func parse(raw string, limit int, include, exclude []string) []block {
	parts := strings.Split(raw, "\n\n")
	out := make([]block, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "goroutine ") {
			continue
		}
		lines := strings.Split(part, "\n")
		if len(lines) < 2 {
			continue
		}
		state := "unknown"
		if start := strings.Index(lines[0], "["); start >= 0 {
			if end := strings.Index(lines[0][start:], "]"); end > 0 {
				state = lines[0][start+1 : start+end]
			}
		}
		stack := make([]string, 0, len(lines)/2)
		for _, line := range lines[1:] {
			if strings.HasPrefix(line, "\t") || strings.TrimSpace(line) == "" {
				continue
			}
			stack = append(stack, strings.TrimSpace(line))
		}
		if len(stack) == 0 {
			continue
		}
		if !matchesFilters(stack, include, exclude) {
			continue
		}
		out = append(out, block{state: state, stack: stack, raw: part})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func matchesFilters(stack, include, exclude []string) bool {
	if len(include) > 0 {
		ok := false
		for _, frame := range stack {
			for _, needle := range include {
				if strings.Contains(frame, needle) {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}

	if len(exclude) == 0 {
		return true
	}
	filtered := false
	for _, frame := range stack {
		skip := false
		for _, prefix := range exclude {
			if strings.HasPrefix(frame, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = true
			break
		}
	}
	return filtered
}

func groupKey(mode string, b block) (string, string) {
	switch mode {
	case "fullStack":
		key := strings.Join(b.stack, "\n")
		return key, firstBusinessFrame(b.stack)
	default:
		frame := firstBusinessFrame(b.stack)
		return frame, frame
	}
}

func firstBusinessFrame(stack []string) string {
	for _, frame := range stack {
		if strings.HasPrefix(frame, "runtime.") {
			continue
		}
		return frame
	}
	if len(stack) == 0 {
		return ""
	}
	return stack[0]
}

func takeFrames(stack []string, n int) []string {
	if len(stack) < n {
		n = len(stack)
	}
	return append([]string(nil), stack[:n]...)
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func nowString(ts int64) []byte {
	return []byte(time.Unix(ts, 0).UTC().Format(time.RFC3339Nano))
}
