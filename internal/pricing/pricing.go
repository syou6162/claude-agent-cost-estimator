// Package pricing computes USD cost from Claude token usage based on a
// hand-curated price table snapshotted from the Anthropic pricing page.
//
// Source: https://platform.claude.com/docs/en/about-claude/pricing
// Snapshot date: 2026-05-16
//
// Update procedure: re-fetch the page above, update modelPricing below, and
// bump the snapshot date.
package pricing

import (
	"regexp"
	"strings"
)

// Tokens is the per-call token count broken down by category.
// CacheCreation5m and CacheCreation1h are used when the ephemeral nested
// form is present; otherwise transcripts populate CacheCreation5m only
// (treated as 5-minute writes per the README/plan caveat).
type Tokens struct {
	Input           int64
	Output          int64
	CacheCreation5m int64
	CacheCreation1h int64
	CacheRead       int64
}

// ModelPrice holds per-MTok prices in USD.
// FastMultiplier is non-nil only for models with a Fast mode SKU.
type ModelPrice struct {
	InputPerMTok   float64
	OutputPerMTok  float64
	CacheWrite5m   float64
	CacheWrite1h   float64
	CacheRead      float64
	FastMultiplier *float64
}

func ptrF64(v float64) *float64 { return &v }

// modelPricing holds the canonical price table keyed by normalized model id.
// Values are USD per 1M tokens.
var modelPricing = map[string]ModelPrice{
	"claude-opus-4-7": {
		InputPerMTok:   5,
		OutputPerMTok:  25,
		CacheWrite5m:   6.25,
		CacheWrite1h:   10,
		CacheRead:      0.5,
		FastMultiplier: ptrF64(6.0),
	},
	"claude-opus-4-6": {
		InputPerMTok:   5,
		OutputPerMTok:  25,
		CacheWrite5m:   6.25,
		CacheWrite1h:   10,
		CacheRead:      0.5,
		FastMultiplier: ptrF64(6.0),
	},
	"claude-opus-4-5": {
		InputPerMTok:  5,
		OutputPerMTok: 25,
		CacheWrite5m:  6.25,
		CacheWrite1h:  10,
		CacheRead:     0.5,
	},
	"claude-opus-4-1": {
		InputPerMTok:  15,
		OutputPerMTok: 75,
		CacheWrite5m:  18.75,
		CacheWrite1h:  30,
		CacheRead:     1.5,
	},
	"claude-opus-4": {
		InputPerMTok:  15,
		OutputPerMTok: 75,
		CacheWrite5m:  18.75,
		CacheWrite1h:  30,
		CacheRead:     1.5,
	},
	"claude-sonnet-4-6": {
		InputPerMTok:  3,
		OutputPerMTok: 15,
		CacheWrite5m:  3.75,
		CacheWrite1h:  6,
		CacheRead:     0.3,
	},
	"claude-sonnet-4-5": {
		InputPerMTok:  3,
		OutputPerMTok: 15,
		CacheWrite5m:  3.75,
		CacheWrite1h:  6,
		CacheRead:     0.3,
	},
	"claude-sonnet-4": {
		InputPerMTok:  3,
		OutputPerMTok: 15,
		CacheWrite5m:  3.75,
		CacheWrite1h:  6,
		CacheRead:     0.3,
	},
	"claude-haiku-4-5": {
		InputPerMTok:  1,
		OutputPerMTok: 5,
		CacheWrite5m:  1.25,
		CacheWrite1h:  2,
		CacheRead:     0.1,
	},
	"claude-haiku-3-5": {
		InputPerMTok:  0.80,
		OutputPerMTok: 4,
		CacheWrite5m:  1.0,
		CacheWrite1h:  1.6,
		CacheRead:     0.08,
	},
}

// modelAliases handles exceptional naming (e.g. {major}-{family} order in
// pre-4.5 ids). Keys are exact input model names; values are normalized keys
// of modelPricing.
var modelAliases = map[string]string{
	"claude-4-opus-20250514":   "claude-opus-4",
	"claude-4-sonnet-20250514": "claude-sonnet-4",
}

// prefixRule normalizes by stripping known prefix shapes (e.g. legacy
// 3.5 family ids). Evaluated in order; first match wins.
type prefixRule struct {
	Prefix     string
	Normalized string
}

var modelPrefixRules = []prefixRule{
	{Prefix: "claude-3-5-sonnet-", Normalized: "claude-sonnet-3-5"},
	{Prefix: "claude-3-5-haiku-", Normalized: "claude-haiku-3-5"},
}

var dateSuffixRe = regexp.MustCompile(`-\d{8}$`)

// resolveModel returns the normalized model id used to look up modelPricing.
// Resolution order: alias map -> price map (after stripping -YYYYMMDD and
// -1m suffixes) -> prefix rules -> "" (unknown).
func resolveModel(name string) string {
	if v, ok := modelAliases[name]; ok {
		return v
	}
	if _, ok := modelPricing[name]; ok {
		return name
	}
	stripped := dateSuffixRe.ReplaceAllString(name, "")
	if _, ok := modelPricing[stripped]; ok {
		return stripped
	}
	if strings.HasSuffix(stripped, "-1m") {
		s2 := strings.TrimSuffix(stripped, "-1m")
		if _, ok := modelPricing[s2]; ok {
			return s2
		}
	}
	for _, r := range modelPrefixRules {
		if strings.HasPrefix(name, r.Prefix) {
			if _, ok := modelPricing[r.Normalized]; ok {
				return r.Normalized
			}
		}
	}
	return ""
}

// CalculateCost returns the USD cost for the given tokens against the model.
// Returns (cost, true) when the model is known; (nil, false) otherwise.
// speed == "fast" multiplies all components by ModelPrice.FastMultiplier
// when it is non-nil for the resolved model.
func CalculateCost(t Tokens, model string, speed string) (*float64, bool) {
	key := resolveModel(model)
	if key == "" {
		return nil, false
	}
	p := modelPricing[key]
	cost := float64(t.Input)*p.InputPerMTok +
		float64(t.Output)*p.OutputPerMTok +
		float64(t.CacheCreation5m)*p.CacheWrite5m +
		float64(t.CacheCreation1h)*p.CacheWrite1h +
		float64(t.CacheRead)*p.CacheRead
	cost /= 1_000_000.0
	if speed == "fast" && p.FastMultiplier != nil {
		cost *= *p.FastMultiplier
	}
	return &cost, true
}
