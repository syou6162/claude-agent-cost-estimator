// Package transcript parses single JSONL lines emitted by Claude Code /
// Claude Agent SDK into Entry records used by aggregation.
//
// Exclusion rules applied here (project-wide spec, see plan):
//   - role != "assistant"
//   - message.model == "<synthetic>"
//   - isApiErrorMessage == true
//
// Cache token resolution: when message.usage.cache_creation is present as
// a nested object, ephemeral_5m_input_tokens / ephemeral_1h_input_tokens
// take precedence and the flat cache_creation_input_tokens is ignored.
// Otherwise the flat field is treated as a 5-minute cache write.
package transcript

import (
	"encoding/json"

	"github.com/syou6162/claude-agent-cost-estimator/internal/pricing"
)

// Entry is one assistant message with usage extracted from a JSONL line.
// SessionID here mirrors what was in the JSONL (informational only; the
// loader determines grouping by file path).
type Entry struct {
	Model     string
	Speed     string
	MessageID string
	RequestID string
	Tokens    pricing.Tokens
	Timestamp string
	Cwd       string
	SessionID string
}

// UniqueHash returns "messageId:requestId" when both are present, or
// "" when either is missing (signaling the entry should not participate
// in dedup).
func (e Entry) UniqueHash() string {
	if e.MessageID == "" || e.RequestID == "" {
		return ""
	}
	return e.MessageID + ":" + e.RequestID
}

type rawCacheCreation struct {
	Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
}

type rawUsage struct {
	InputTokens              int64             `json:"input_tokens"`
	OutputTokens             int64             `json:"output_tokens"`
	CacheCreationInputTokens int64             `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64             `json:"cache_read_input_tokens"`
	CacheCreation            *rawCacheCreation `json:"cache_creation,omitempty"`
	Speed                    string            `json:"speed,omitempty"`
}

type rawMessage struct {
	Role  string    `json:"role"`
	Model string    `json:"model"`
	ID    string    `json:"id"`
	Usage *rawUsage `json:"usage,omitempty"`
}

type rawLine struct {
	Type              string     `json:"type"`
	Message           rawMessage `json:"message"`
	RequestID         string     `json:"requestId"`
	IsApiErrorMessage bool       `json:"isApiErrorMessage"`
	Timestamp         string     `json:"timestamp"`
	Cwd               string     `json:"cwd"`
	SessionID         string     `json:"sessionId"`
}

// ParseLine decodes a single JSONL row and returns an Entry when the row
// represents an assistant message with usage that is not synthetic or an
// API error. Returns (nil, nil) for rows that are well-formed but skipped
// by the exclusion rules.
func ParseLine(data []byte) (*Entry, error) {
	var l rawLine
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Message.Role != "assistant" {
		return nil, nil
	}
	if l.Message.Usage == nil {
		return nil, nil
	}
	if l.Message.Model == "<synthetic>" {
		return nil, nil
	}
	if l.IsApiErrorMessage {
		return nil, nil
	}

	tokens := pricing.Tokens{
		Input:     l.Message.Usage.InputTokens,
		Output:    l.Message.Usage.OutputTokens,
		CacheRead: l.Message.Usage.CacheReadInputTokens,
	}
	if l.Message.Usage.CacheCreation != nil {
		tokens.CacheCreation5m = l.Message.Usage.CacheCreation.Ephemeral5m
		tokens.CacheCreation1h = l.Message.Usage.CacheCreation.Ephemeral1h
	} else {
		tokens.CacheCreation5m = l.Message.Usage.CacheCreationInputTokens
	}

	return &Entry{
		Model:     l.Message.Model,
		Speed:     l.Message.Usage.Speed,
		MessageID: l.Message.ID,
		RequestID: l.RequestID,
		Tokens:    tokens,
		Timestamp: l.Timestamp,
		Cwd:       l.Cwd,
		SessionID: l.SessionID,
	}, nil
}

// ExtractCwdField pulls the top-level "cwd" field from a single JSONL row
// without fully unmarshaling the rest. Used by the loader's fallback
// project-directory resolution.
func ExtractCwdField(data []byte) (string, bool) {
	var probe struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", false
	}
	if probe.Cwd == "" {
		return "", false
	}
	return probe.Cwd, true
}
