// Package aggregate collapses parsed transcript entries into per-session
// JSON records suitable for downstream jq processing.
package aggregate

import (
	"sort"

	"github.com/syou6162/claude-agent-cost-estimator/internal/pricing"
	"github.com/syou6162/claude-agent-cost-estimator/internal/transcript"
)

// FileEntry pairs a parsed transcript entry with the file / line it came
// from so dedup can pick a deterministic survivor and the WARN emitter
// can name the first occurrence of any unknown model.
type FileEntry struct {
	FilePath   string
	LineNumber int
	SessionID  string
	Entry      transcript.Entry
}

// ModelBreakdown is one (model, speed) row in a session's report.
type ModelBreakdown struct {
	Model                    string   `json:"model"`
	Speed                    string   `json:"speed"`
	InputTokens              int64    `json:"inputTokens"`
	OutputTokens             int64    `json:"outputTokens"`
	CacheCreationInputTokens int64    `json:"cacheCreationInputTokens"`
	CacheCreation5mTokens    int64    `json:"cacheCreation5mInputTokens"`
	CacheCreation1hTokens    int64    `json:"cacheCreation1hInputTokens"`
	CacheReadInputTokens     int64    `json:"cacheReadInputTokens"`
	CostUSD                  *float64 `json:"costUSD"`
}

// SessionReport is the per-session output object.
type SessionReport struct {
	SessionID      string           `json:"sessionId"`
	ProjectPath    string           `json:"projectPath"`
	FirstTimestamp string           `json:"firstTimestamp"`
	LastTimestamp  string           `json:"lastTimestamp"`
	Models         []ModelBreakdown `json:"models"`
	TotalCostUSD   *float64         `json:"totalCostUSD"`
	KnownCostUSD   *float64         `json:"knownCostUSD"`
}

// UnknownModelOccurrence is the first (filePath asc, lineNumber asc)
// sighting of an unknown model, used to drive the deterministic WARN
// output.
type UnknownModelOccurrence struct {
	Model      string
	SessionID  string
	FilePath   string
	LineNumber int
}

// Dedup applies ccusage's shouldReplaceEntryMetadata rule to entries
// sharing a UniqueHash: keep the survivor with the larger tokenTotal,
// breaking ties in favor of an entry whose speed field is non-empty.
// Entries with an empty UniqueHash are passed through unchanged.
func Dedup(in []FileEntry) []FileEntry {
	out := make([]FileEntry, 0, len(in))
	// Map from hash to index in `out`.
	idx := map[string]int{}
	for _, fe := range in {
		hash := fe.Entry.UniqueHash()
		if hash == "" {
			out = append(out, fe)
			continue
		}
		existingPos, ok := idx[hash]
		if !ok {
			idx[hash] = len(out)
			out = append(out, fe)
			continue
		}
		if shouldReplace(fe, out[existingPos]) {
			out[existingPos] = fe
		}
	}
	return out
}

func shouldReplace(candidate, existing FileEntry) bool {
	cTotal := tokenTotal(candidate.Entry.Tokens)
	eTotal := tokenTotal(existing.Entry.Tokens)
	if cTotal != eTotal {
		return cTotal > eTotal
	}
	return candidate.Entry.Speed != "" && existing.Entry.Speed == ""
}

func tokenTotal(t pricing.Tokens) int64 {
	return t.Input + t.Output + t.CacheCreation5m + t.CacheCreation1h + t.CacheRead
}

// Aggregate groups entries by sessionId, computes model breakdowns and
// total costs, and returns sessions sorted by firstTimestamp then
// sessionId.
func Aggregate(entries []FileEntry) []SessionReport {
	type modelKey struct{ Model, Speed string }
	type sessionAcc struct {
		first, last string
		models      map[modelKey]*ModelBreakdown
	}
	sessions := map[string]*sessionAcc{}
	for _, fe := range entries {
		s, ok := sessions[fe.SessionID]
		if !ok {
			s = &sessionAcc{
				first:  fe.Entry.Timestamp,
				last:   fe.Entry.Timestamp,
				models: map[modelKey]*ModelBreakdown{},
			}
			sessions[fe.SessionID] = s
		}
		if fe.Entry.Timestamp < s.first {
			s.first = fe.Entry.Timestamp
		}
		if fe.Entry.Timestamp > s.last {
			s.last = fe.Entry.Timestamp
		}
		k := modelKey{Model: fe.Entry.Model, Speed: normalizeSpeed(fe.Entry.Speed)}
		mb, ok := s.models[k]
		if !ok {
			mb = &ModelBreakdown{Model: k.Model, Speed: k.Speed}
			s.models[k] = mb
		}
		mb.InputTokens += fe.Entry.Tokens.Input
		mb.OutputTokens += fe.Entry.Tokens.Output
		mb.CacheCreation5mTokens += fe.Entry.Tokens.CacheCreation5m
		mb.CacheCreation1hTokens += fe.Entry.Tokens.CacheCreation1h
		mb.CacheReadInputTokens += fe.Entry.Tokens.CacheRead
	}
	reports := make([]SessionReport, 0, len(sessions))
	for sid, s := range sessions {
		mbs := make([]ModelBreakdown, 0, len(s.models))
		for _, mb := range s.models {
			mb.CacheCreationInputTokens = mb.CacheCreation5mTokens + mb.CacheCreation1hTokens
			cost, _ := pricing.CalculateCost(pricing.Tokens{
				Input:           mb.InputTokens,
				Output:          mb.OutputTokens,
				CacheCreation5m: mb.CacheCreation5mTokens,
				CacheCreation1h: mb.CacheCreation1hTokens,
				CacheRead:       mb.CacheReadInputTokens,
			}, mb.Model, mb.Speed)
			mb.CostUSD = cost
			mbs = append(mbs, *mb)
		}
		sort.Slice(mbs, func(i, j int) bool {
			if mbs[i].Model != mbs[j].Model {
				return mbs[i].Model < mbs[j].Model
			}
			return mbs[i].Speed < mbs[j].Speed
		})
		var total *float64
		var known float64
		allKnown := true
		for _, mb := range mbs {
			if mb.CostUSD == nil {
				allKnown = false
				continue
			}
			known += *mb.CostUSD
		}
		k := known
		knownPtr := &k
		if allKnown {
			t := known
			total = &t
		}
		reports = append(reports, SessionReport{
			SessionID:      sid,
			FirstTimestamp: s.first,
			LastTimestamp:  s.last,
			Models:         mbs,
			TotalCostUSD:   total,
			KnownCostUSD:   knownPtr,
		})
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].FirstTimestamp != reports[j].FirstTimestamp {
			return reports[i].FirstTimestamp < reports[j].FirstTimestamp
		}
		return reports[i].SessionID < reports[j].SessionID
	})
	return reports
}

func normalizeSpeed(s string) string {
	if s == "" {
		return "standard"
	}
	return s
}

// CollectUnknownModels returns one occurrence per unknown model, using
// the file path ascending then line number ascending order as the
// tiebreaker. The result is sorted by model name.
func CollectUnknownModels(entries []FileEntry) []UnknownModelOccurrence {
	// Find unknown models by trying to price each entry once.
	sorted := make([]FileEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].FilePath != sorted[j].FilePath {
			return sorted[i].FilePath < sorted[j].FilePath
		}
		return sorted[i].LineNumber < sorted[j].LineNumber
	})
	seen := map[string]UnknownModelOccurrence{}
	for _, fe := range sorted {
		_, known := pricing.CalculateCost(fe.Entry.Tokens, fe.Entry.Model, fe.Entry.Speed)
		if known {
			continue
		}
		if _, ok := seen[fe.Entry.Model]; ok {
			continue
		}
		seen[fe.Entry.Model] = UnknownModelOccurrence{
			Model:      fe.Entry.Model,
			SessionID:  fe.SessionID,
			FilePath:   fe.FilePath,
			LineNumber: fe.LineNumber,
		}
	}
	out := make([]UnknownModelOccurrence, 0, len(seen))
	for _, occ := range seen {
		out = append(out, occ)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}
