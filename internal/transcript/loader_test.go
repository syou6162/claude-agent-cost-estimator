package transcript

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// EncodeCwd converts an absolute cwd to the Claude project directory name
// by replacing every "/" and "." with "-".
func TestEncodeCwd(t *testing.T) {
	got := EncodeCwd("/Users/foo/work/repo")
	want := "-Users-foo-work-repo"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Dots in path components must also be encoded to "-" (observed Claude
// behavior, e.g. yasuhisa.yoshida -> yasuhisa-yoshida).
func TestEncodeCwd_DotsReplaced(t *testing.T) {
	got := EncodeCwd("/Users/foo.bar/work/repo")
	want := "-Users-foo-bar-work-repo"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// EnumerateDataRoots: with no env, defaults to ${XDG_CONFIG_HOME:-~/.config}/claude
// and ~/.claude. Only roots whose <root>/projects exists are returned.
func TestEnumerateDataRoots_Defaults(t *testing.T) {
	tmp := t.TempDir()
	cfgClaude := filepath.Join(tmp, "config", "claude")
	homeClaude := filepath.Join(tmp, "home", ".claude")
	// Only homeClaude has /projects under it.
	mustMkdir(t, filepath.Join(homeClaude, "projects"))
	mustMkdir(t, cfgClaude) // no projects/

	roots, err := EnumerateDataRoots(EnumerateOptions{
		XDGConfigHome: filepath.Join(tmp, "config"),
		HomeDir:       filepath.Join(tmp, "home"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{homeClaude}
	if !reflect.DeepEqual(roots, want) {
		t.Errorf("got %v want %v", roots, want)
	}
}

// EnumerateDataRoots: env explicitly set with all-invalid paths returns ErrNoValidConfigDir.
func TestEnumerateDataRoots_ExplicitAllInvalid(t *testing.T) {
	tmp := t.TempDir()
	bad1 := filepath.Join(tmp, "nope")
	bad2 := filepath.Join(tmp, "alsoNope")
	_, err := EnumerateDataRoots(EnumerateOptions{
		ExplicitConfigDirs: []string{bad1, bad2},
	})
	if err == nil {
		t.Fatal("expected error for all-invalid explicit dirs")
	}
}

// EnumerateDataRoots: env explicitly set, at least one valid path.
func TestEnumerateDataRoots_ExplicitMixed(t *testing.T) {
	tmp := t.TempDir()
	good := filepath.Join(tmp, "good")
	bad := filepath.Join(tmp, "nope")
	mustMkdir(t, filepath.Join(good, "projects"))
	roots, err := EnumerateDataRoots(EnumerateOptions{
		ExplicitConfigDirs: []string{bad, good},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roots, []string{good}) {
		t.Errorf("got %v want [%v]", roots, good)
	}
}

// ResolveProjectDir: direct encoded-name hit.
func TestResolveProjectDir_EncodedHit(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	encoded := "-Users-x-y"
	mustMkdir(t, filepath.Join(root, "projects", encoded))
	dir, err := ResolveProjectDir([]string{root}, "/Users/x/y")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "projects", encoded) {
		t.Errorf("got %q", dir)
	}
}

// ResolveProjectDir: fallback by scanning JSONL cwd field when encoded
// directory does not exist.
func TestResolveProjectDir_FallbackByCwd(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	other := filepath.Join(root, "projects", "totally-other-name")
	mustMkdir(t, other)
	mustWriteFile(t, filepath.Join(other, "abc.jsonl"),
		`{"type":"x"}`+"\n"+
			`{"type":"assistant","cwd":"/real/cwd","message":{"role":"assistant","model":"m","id":"a","usage":{"input_tokens":1}},"requestId":"r"}`+"\n")
	dir, err := ResolveProjectDir([]string{root}, "/real/cwd")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dir != other {
		t.Errorf("got %q want %q", dir, other)
	}
}

// ResolveProjectDir: not found at all.
func TestResolveProjectDir_NotFound(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	mustMkdir(t, filepath.Join(root, "projects"))
	_, err := ResolveProjectDir([]string{root}, "/no/match")
	if err == nil {
		t.Fatal("expected error")
	}
}

// CollectJSONLFiles: recursive scan, returns sorted file paths.
func TestCollectJSONLFiles(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "a.jsonl"), "")
	mustWriteFile(t, filepath.Join(tmp, "sub", "b.jsonl"), "")
	mustWriteFile(t, filepath.Join(tmp, "sub", "c.txt"), "ignored")
	got, err := CollectJSONLFiles(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{filepath.Join(tmp, "a.jsonl"), filepath.Join(tmp, "sub", "b.jsonl")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// DeriveSessionID: nested form takes the parent directory name.
func TestDeriveSessionID_Nested(t *testing.T) {
	got := DeriveSessionID("/x/projects/proj/abc123/chunk-0.jsonl", "/x/projects/proj")
	if got != "abc123" {
		t.Errorf("got %q", got)
	}
}

// DeriveSessionID: flat form takes the basename without extension.
func TestDeriveSessionID_Flat(t *testing.T) {
	got := DeriveSessionID("/x/projects/proj/abc123.jsonl", "/x/projects/proj")
	if got != "abc123" {
		t.Errorf("got %q", got)
	}
}

// --- helpers ---

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, p string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
