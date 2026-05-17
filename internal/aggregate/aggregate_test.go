package aggregate

import (
	"math"
	"testing"

	"github.com/syou6162/claude-agent-cost-estimator/internal/pricing"
	"github.com/syou6162/claude-agent-cost-estimator/internal/transcript"
)

func mkEntry(sid, model, msgID, reqID string, in, out int64) FileEntry {
	return FileEntry{
		FilePath:   "/x/" + sid + "/" + msgID + ".jsonl",
		LineNumber: 1,
		SessionID:  sid,
		Entry: transcript.Entry{
			Model:     model,
			MessageID: msgID,
			RequestID: reqID,
			Tokens:    pricing.Tokens{Input: in, Output: out},
			Timestamp: "2026-04-15T22:00:00.000Z",
		},
	}
}

// Same hash, second has larger tokenTotal → replace.
func TestDedup_LargerTokensWin(t *testing.T) {
	a := mkEntry("s1", "claude-opus-4-7", "m", "r", 100, 200)
	b := mkEntry("s1", "claude-opus-4-7", "m", "r", 150, 200)
	out := Dedup([]FileEntry{a, b})
	if len(out) != 1 {
		t.Fatalf("got %d", len(out))
	}
	if out[0].Entry.Tokens.Input != 150 {
		t.Errorf("wrong entry kept: %+v", out[0])
	}
}

// Same hash, same tokens, hasSpeed wins.
func TestDedup_HasSpeedWins(t *testing.T) {
	a := mkEntry("s1", "claude-opus-4-7", "m", "r", 100, 200)
	b := mkEntry("s1", "claude-opus-4-7", "m", "r", 100, 200)
	b.Entry.Speed = "standard"
	out := Dedup([]FileEntry{a, b})
	if len(out) != 1 {
		t.Fatalf("got %d", len(out))
	}
	if out[0].Entry.Speed != "standard" {
		t.Errorf("hasSpeed candidate should win, got %+v", out[0].Entry.Speed)
	}
}

// Missing hash entries are always kept (no dedup).
func TestDedup_EmptyHashKept(t *testing.T) {
	a := mkEntry("s1", "claude-opus-4-7", "", "", 1, 2)
	b := mkEntry("s1", "claude-opus-4-7", "", "", 3, 4)
	out := Dedup([]FileEntry{a, b})
	if len(out) != 2 {
		t.Errorf("expected 2, got %d", len(out))
	}
}

// Session aggregation: per-(model, speed) breakdown + costs.
func TestAggregate_BasicSession(t *testing.T) {
	entries := []FileEntry{
		mkEntry("s1", "claude-opus-4-7", "m1", "r1", 1_000_000, 1_000_000),
		mkEntry("s1", "claude-sonnet-4-6", "m2", "r2", 100_000, 100_000),
	}
	out := Aggregate(entries)
	if len(out) != 1 {
		t.Fatalf("got %d sessions", len(out))
	}
	s := out[0]
	if s.SessionID != "s1" {
		t.Errorf("sid: %s", s.SessionID)
	}
	if len(s.Models) != 2 {
		t.Fatalf("models: %d", len(s.Models))
	}
	// Models are sorted by name asc
	if s.Models[0].Model != "claude-opus-4-7" {
		t.Errorf("models[0]: %s", s.Models[0].Model)
	}
	if s.TotalCostUSD == nil {
		t.Fatal("totalCostUSD nil")
	}
	want := 30.0 + 1.8 // opus 30 + sonnet 1.8
	if math.Abs(*s.TotalCostUSD-want) > 1e-9 {
		t.Errorf("got %v want %v", *s.TotalCostUSD, want)
	}
}

// Every model unknown → both totalCostUSD and knownCostUSD are nil
// so the JSON output distinguishes "no known cost data" from "$0".
func TestAggregate_AllUnknownNullsBoth(t *testing.T) {
	entries := []FileEntry{
		mkEntry("s1", "totally-unknown-1", "m1", "r1", 1_000_000, 0),
		mkEntry("s1", "totally-unknown-2", "m2", "r2", 1_000_000, 0),
	}
	out := Aggregate(entries)
	s := out[0]
	if s.TotalCostUSD != nil {
		t.Errorf("totalCostUSD should be nil, got %v", *s.TotalCostUSD)
	}
	if s.KnownCostUSD != nil {
		t.Errorf("knownCostUSD should be nil when no model priced, got %v", *s.KnownCostUSD)
	}
}

// Unknown model → costUSD nil, totalCostUSD nil, knownCostUSD reflects known portion.
func TestAggregate_UnknownModelNullsTotal(t *testing.T) {
	entries := []FileEntry{
		mkEntry("s1", "claude-opus-4-7", "m1", "r1", 1_000_000, 0),
		mkEntry("s1", "totally-unknown", "m2", "r2", 1_000_000, 0),
	}
	out := Aggregate(entries)
	s := out[0]
	if s.TotalCostUSD != nil {
		t.Errorf("totalCostUSD should be nil, got %v", *s.TotalCostUSD)
	}
	if s.KnownCostUSD == nil {
		t.Fatal("knownCostUSD should be non-nil")
	}
	if math.Abs(*s.KnownCostUSD-5.0) > 1e-9 {
		t.Errorf("known: got %v want 5.0", *s.KnownCostUSD)
	}
	// Verify per-model: unknown one is nil.
	var unkSeen bool
	for _, m := range s.Models {
		if m.Model == "totally-unknown" {
			if m.CostUSD != nil {
				t.Errorf("unknown costUSD should be nil")
			}
			unkSeen = true
		}
	}
	if !unkSeen {
		t.Error("unknown model not present")
	}
}

// Sessions sorted by firstTimestamp asc (then sessionId).
func TestAggregate_SessionsSorted(t *testing.T) {
	e1 := mkEntry("s2", "claude-opus-4-7", "m1", "r1", 1, 1)
	e1.Entry.Timestamp = "2026-04-15T22:00:00.000Z"
	e2 := mkEntry("s1", "claude-opus-4-7", "m2", "r2", 1, 1)
	e2.Entry.Timestamp = "2026-04-15T21:00:00.000Z"
	out := Aggregate([]FileEntry{e1, e2})
	if len(out) != 2 {
		t.Fatalf("got %d", len(out))
	}
	if out[0].SessionID != "s1" {
		t.Errorf("s1 should be first; got %s", out[0].SessionID)
	}
}

// CollectUnknownModels returns deterministic (model, sessionId) pairs
// using filePath asc then lineNumber asc, with one entry per model.
func TestCollectUnknownModels_Deterministic(t *testing.T) {
	// Insert in reverse order on purpose.
	e1 := mkEntry("sB", "foo", "m1", "r1", 1, 1)
	e1.FilePath = "/zzz/sB/x.jsonl"
	e1.LineNumber = 5
	e2 := mkEntry("sA", "foo", "m2", "r2", 1, 1)
	e2.FilePath = "/aaa/sA/x.jsonl"
	e2.LineNumber = 1
	e3 := mkEntry("sA", "bar", "m3", "r3", 1, 1)
	e3.FilePath = "/aaa/sA/x.jsonl"
	e3.LineNumber = 2

	got := CollectUnknownModels([]FileEntry{e1, e2, e3})
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	// Sorted by model name asc.
	if got[0].Model != "bar" || got[1].Model != "foo" {
		t.Errorf("model order wrong: %+v", got)
	}
	// First sighting of "foo" should be /aaa/sA/x.jsonl line 1 (sA), not /zzz.
	if got[1].SessionID != "sA" {
		t.Errorf("foo first-sighting sessionId wrong: got %s", got[1].SessionID)
	}
}

// First/last timestamp tracked.
func TestAggregate_TimestampRange(t *testing.T) {
	a := mkEntry("s1", "claude-opus-4-7", "m1", "r1", 1, 1)
	a.Entry.Timestamp = "2026-04-15T22:00:00.000Z"
	b := mkEntry("s1", "claude-opus-4-7", "m2", "r2", 1, 1)
	b.Entry.Timestamp = "2026-04-15T22:30:00.000Z"
	out := Aggregate([]FileEntry{b, a}) // reversed insert
	s := out[0]
	if s.FirstTimestamp != "2026-04-15T22:00:00.000Z" {
		t.Errorf("first: %s", s.FirstTimestamp)
	}
	if s.LastTimestamp != "2026-04-15T22:30:00.000Z" {
		t.Errorf("last: %s", s.LastTimestamp)
	}
}
