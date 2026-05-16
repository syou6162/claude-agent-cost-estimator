package pricing

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// T1: claude-opus-4-7 with all token types
func TestCalculateCost_Opus47_AllTokens(t *testing.T) {
	cost, known := CalculateCost(Tokens{
		Input:           1_000_000,
		Output:          1_000_000,
		CacheCreation5m: 1_000_000,
		CacheRead:       1_000_000,
	}, "claude-opus-4-7", "standard")
	if !known {
		t.Fatal("expected known model")
	}
	if cost == nil {
		t.Fatal("expected non-nil cost")
	}
	// 5 + 25 + 6.25 + 0.5 = 36.75
	if !almostEqual(*cost, 36.75) {
		t.Errorf("got %v, want 36.75", *cost)
	}
}

// T2: claude-sonnet-4-6 standard
func TestCalculateCost_Sonnet46_Standard(t *testing.T) {
	cost, known := CalculateCost(Tokens{
		Input:  100_000,
		Output: 100_000,
	}, "claude-sonnet-4-6", "standard")
	if !known || cost == nil {
		t.Fatalf("known=%v cost=%v", known, cost)
	}
	// 0.3 + 1.5 = 1.8
	if !almostEqual(*cost, 1.8) {
		t.Errorf("got %v, want 1.8", *cost)
	}
}

// T3: fast multiplier for opus 4-7
func TestCalculateCost_Opus47_Fast(t *testing.T) {
	cost, _ := CalculateCost(Tokens{
		Input:  1_000_000,
		Output: 1_000_000,
	}, "claude-opus-4-7", "fast")
	// (5 + 25) * 6 = 180
	if !almostEqual(*cost, 180.0) {
		t.Errorf("got %v, want 180.0", *cost)
	}
}

// T3b: fast multiplier should NOT apply to non-opus-4-7/4-6 models (e.g., sonnet)
func TestCalculateCost_Sonnet_FastIgnored(t *testing.T) {
	costStd, _ := CalculateCost(Tokens{Input: 1_000_000}, "claude-sonnet-4-6", "standard")
	costFast, _ := CalculateCost(Tokens{Input: 1_000_000}, "claude-sonnet-4-6", "fast")
	// sonnet has no fast multiplier per Anthropic pricing
	if !almostEqual(*costStd, *costFast) {
		t.Errorf("sonnet fast should equal standard: std=%v fast=%v", *costStd, *costFast)
	}
}

// T4a: date suffix stripping
func TestCalculateCost_DateSuffixStripped(t *testing.T) {
	cost, known := CalculateCost(Tokens{Input: 1_000_000}, "claude-opus-4-7-20251101", "standard")
	if !known || cost == nil {
		t.Fatalf("expected hit for date-suffixed model, known=%v", known)
	}
	if !almostEqual(*cost, 5.0) {
		t.Errorf("got %v, want 5.0", *cost)
	}
}

// T4b: -1m suffix stripping
func TestCalculateCost_1mSuffixStripped(t *testing.T) {
	cost, known := CalculateCost(Tokens{Input: 1_000_000}, "claude-opus-4-7-1m", "standard")
	if !known || cost == nil {
		t.Fatalf("expected hit for -1m suffix, known=%v", known)
	}
	if !almostEqual(*cost, 5.0) {
		t.Errorf("got %v, want 5.0", *cost)
	}
}

// T4c: -1m + date together
func TestCalculateCost_1mAndDateSuffix(t *testing.T) {
	cost, known := CalculateCost(Tokens{Input: 1_000_000}, "claude-sonnet-4-6-1m-20251101", "standard")
	if !known || cost == nil {
		t.Fatalf("expected hit, known=%v", known)
	}
	if !almostEqual(*cost, 3.0) {
		t.Errorf("got %v, want 3.0", *cost)
	}
}

// T4d: alias (claude-4-sonnet-YYYYMMDD style)
func TestCalculateCost_AliasMap(t *testing.T) {
	cost, known := CalculateCost(Tokens{Input: 1_000_000}, "claude-4-sonnet-20250514", "standard")
	if !known {
		t.Fatal("expected alias hit")
	}
	// claude-sonnet-4 (deprecated) uses sonnet 4 pricing = $3/MTok input
	if !almostEqual(*cost, 3.0) {
		t.Errorf("got %v, want 3.0", *cost)
	}
}

// E1: unknown model
func TestCalculateCost_UnknownModel(t *testing.T) {
	cost, known := CalculateCost(Tokens{Input: 1_000_000}, "claude-zeta-99-99", "standard")
	if known {
		t.Error("expected unknown")
	}
	if cost != nil {
		t.Errorf("expected nil cost for unknown model, got %v", *cost)
	}
}

// Cache 5m write pricing (1.25x base)
func TestCalculateCost_Opus47_CacheWrite5m(t *testing.T) {
	cost, _ := CalculateCost(Tokens{CacheCreation5m: 1_000_000}, "claude-opus-4-7", "standard")
	// 1.25 * 5 = 6.25
	if !almostEqual(*cost, 6.25) {
		t.Errorf("got %v, want 6.25", *cost)
	}
}

// Cache 1h write pricing (2x base) — applies when ephemeral_1h is present
func TestCalculateCost_Opus47_CacheWrite1h(t *testing.T) {
	cost, _ := CalculateCost(Tokens{CacheCreation1h: 1_000_000}, "claude-opus-4-7", "standard")
	// 2 * 5 = 10
	if !almostEqual(*cost, 10.0) {
		t.Errorf("got %v, want 10.0", *cost)
	}
}

// Haiku 4.5 ephemeral combined
func TestCalculateCost_Haiku45_EphemeralCombined(t *testing.T) {
	cost, known := CalculateCost(Tokens{
		Input:           10,
		Output:          20,
		CacheCreation5m: 100,
		CacheCreation1h: 200,
		CacheRead:       50,
	}, "claude-haiku-4-5", "standard")
	if !known || cost == nil {
		t.Fatalf("known=%v cost=%v", known, cost)
	}
	// 10*1/1M + 20*5/1M + 100*1.25/1M + 200*2/1M + 50*0.1/1M
	// = 0.00001 + 0.0001 + 0.000125 + 0.0004 + 0.000005
	want := (10*1.0 + 20*5.0 + 100*1.25 + 200*2.0 + 50*0.1) / 1_000_000
	if !almostEqual(*cost, want) {
		t.Errorf("got %v, want %v", *cost, want)
	}
}
