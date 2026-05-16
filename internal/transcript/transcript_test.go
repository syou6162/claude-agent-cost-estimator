package transcript

import (
	"strings"
	"testing"
)

// ParseLine should accept assistant entries with usage and surface tokens.
func TestParseLine_AssistantWithUsage(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-7","id":"msg_1","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":40}},"requestId":"req_1","timestamp":"2026-04-15T22:00:00.000Z","cwd":"/test","sessionId":"sess-1"}`
	e, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if e.Model != "claude-opus-4-7" {
		t.Errorf("model: got %q", e.Model)
	}
	if e.Tokens.Input != 10 || e.Tokens.Output != 20 {
		t.Errorf("tokens: %+v", e.Tokens)
	}
	// flat cache_creation_input_tokens treated as 5m write
	if e.Tokens.CacheCreation5m != 30 {
		t.Errorf("cache 5m: got %d", e.Tokens.CacheCreation5m)
	}
	if e.Tokens.CacheCreation1h != 0 {
		t.Errorf("cache 1h: got %d", e.Tokens.CacheCreation1h)
	}
	if e.Tokens.CacheRead != 40 {
		t.Errorf("cache read: got %d", e.Tokens.CacheRead)
	}
	if e.UniqueHash() != "msg_1:req_1" {
		t.Errorf("uniqueHash: %q", e.UniqueHash())
	}
}

// Role != assistant must be excluded.
func TestParseLine_UserRoleExcluded(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":"hi","usage":{"input_tokens":1}}}`
	e, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if e != nil {
		t.Errorf("expected nil for user role, got %+v", e)
	}
}

// Missing usage must be filtered out.
func TestParseLine_NoUsage(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"x","id":"a"}}`
	e, _ := ParseLine([]byte(line))
	if e != nil {
		t.Errorf("expected nil when usage missing, got %+v", e)
	}
}

// <synthetic> model entries are excluded.
func TestParseLine_SyntheticExcluded(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","id":"a","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"r"}`
	e, _ := ParseLine([]byte(line))
	if e != nil {
		t.Errorf("expected nil for <synthetic>, got %+v", e)
	}
}

// isApiErrorMessage true entries are excluded.
func TestParseLine_ApiErrorExcluded(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-7","id":"a","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"r","isApiErrorMessage":true}`
	e, _ := ParseLine([]byte(line))
	if e != nil {
		t.Errorf("expected nil for isApiErrorMessage, got %+v", e)
	}
}

// Ephemeral nested form: nested values win, flat is ignored.
func TestParseLine_EphemeralNestedPreferred(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"claude-haiku-4-5","id":"a","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":999,"cache_read_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200}}},"requestId":"r"}`
	e, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if e == nil {
		t.Fatal("nil")
	}
	if e.Tokens.CacheCreation5m != 100 {
		t.Errorf("5m: got %d, want 100", e.Tokens.CacheCreation5m)
	}
	if e.Tokens.CacheCreation1h != 200 {
		t.Errorf("1h: got %d, want 200", e.Tokens.CacheCreation1h)
	}
}

// UniqueHash is empty when messageId or requestId is missing.
func TestParseLine_MissingIDsGivesEmptyHash(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"x","id":"only-msg","usage":{"input_tokens":1}}}`
	e, _ := ParseLine([]byte(line))
	if e == nil {
		t.Fatal("nil")
	}
	if e.UniqueHash() != "" {
		t.Errorf("expected empty hash, got %q", e.UniqueHash())
	}
}

// Malformed JSON returns an error and nil entry.
func TestParseLine_Malformed(t *testing.T) {
	line := `{"type":"assistant","message":{`
	e, err := ParseLine([]byte(line))
	if err == nil {
		t.Error("expected error")
	}
	if e != nil {
		t.Errorf("expected nil entry, got %+v", e)
	}
}

// Fast speed surfaces through.
func TestParseLine_FastSpeed(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-7","id":"a","usage":{"input_tokens":1,"output_tokens":1,"speed":"fast"}},"requestId":"r"}`
	e, _ := ParseLine([]byte(line))
	if e == nil {
		t.Fatal("nil")
	}
	if e.Speed != "fast" {
		t.Errorf("speed: %q", e.Speed)
	}
}

// Timestamp and cwd come through as-is.
func TestParseLine_Metadata(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"x","id":"a","usage":{"input_tokens":1}},"requestId":"r","timestamp":"2026-04-15T22:00:00.000Z","cwd":"/some/where"}`
	e, _ := ParseLine([]byte(line))
	if e == nil {
		t.Fatal("nil")
	}
	if e.Timestamp != "2026-04-15T22:00:00.000Z" {
		t.Errorf("ts: %q", e.Timestamp)
	}
	if e.Cwd != "/some/where" {
		t.Errorf("cwd: %q", e.Cwd)
	}
}

// ExtractCwdFromFirstLine helper finds cwd anywhere in a small file scan.
func TestExtractCwdField(t *testing.T) {
	line := `{"type":"assistant","cwd":"/abc/def","other":1}`
	cwd, ok := ExtractCwdField([]byte(line))
	if !ok || cwd != "/abc/def" {
		t.Errorf("got %q ok=%v", cwd, ok)
	}
}

func TestExtractCwdField_Missing(t *testing.T) {
	_, ok := ExtractCwdField([]byte(`{"type":"x"}`))
	if ok {
		t.Error("expected !ok")
	}
}

// Smoke: parsing a multi-line buffer should ignore non-assistant lines.
func TestParseLine_RealWorldMix(t *testing.T) {
	user := `{"type":"user","message":{"role":"user","content":"hi"},"timestamp":"x"}`
	asst := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-7","id":"a","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"r"}`
	for i, l := range []string{user, asst} {
		_, err := ParseLine([]byte(l))
		if err != nil {
			t.Errorf("line %d: unexpected err %v", i, err)
		}
	}
	if !strings.HasPrefix(asst, "{") {
		t.Fatal("guard")
	}
}
