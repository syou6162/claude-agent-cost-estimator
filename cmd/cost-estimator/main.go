// Command cost-estimator reads Claude Code / Claude Agent SDK transcripts
// for a given working directory and prints per-session cost breakdowns
// as JSON on stdout. See README.md for usage.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syou6162/claude-agent-cost-estimator/internal/aggregate"
	"github.com/syou6162/claude-agent-cost-estimator/internal/transcript"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("cost-estimator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cwd := fs.String("cwd", "", "absolute working directory whose transcripts to aggregate (required)")
	configDir := fs.String("claude-config-dir", "", "comma-separated config dirs (overrides CLAUDE_CONFIG_DIR; default: ${XDG_CONFIG_HOME:-~/.config}/claude,~/.claude)")
	pretty := fs.Bool("pretty", false, "pretty-print JSON output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *cwd == "" {
		fmt.Fprintln(stderr, "usage: --cwd is required")
		return exitUsage
	}
	absCwd, err := filepath.Abs(*cwd)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --cwd: %v\n", err)
		return exitUsage
	}

	opts := transcript.EnumerateOptions{
		ExplicitConfigDirs: resolveExplicitConfigDirs(*configDir, os.Getenv("CLAUDE_CONFIG_DIR")),
		XDGConfigHome:      os.Getenv("XDG_CONFIG_HOME"),
		HomeDir:            os.Getenv("HOME"),
	}
	roots, err := transcript.EnumerateDataRoots(opts)
	if err != nil {
		if errors.Is(err, transcript.ErrNoValidConfigDir) {
			fmt.Fprintln(stderr, "no valid claude config dir (need <path>/projects)")
		} else {
			fmt.Fprintf(stderr, "config dir error: %v\n", err)
		}
		return exitUsage
	}
	if len(roots) == 0 {
		fmt.Fprintln(stderr, "no valid claude config dir (need <path>/projects)")
		return exitUsage
	}

	projectDir, err := transcript.ResolveProjectDir(roots, absCwd)
	if err != nil {
		fmt.Fprintf(stderr, "no project dir for cwd: %s\n", absCwd)
		return exitUsage
	}

	files, err := transcript.CollectJSONLFiles(projectDir)
	if err != nil {
		fmt.Fprintf(stderr, "scan error: %v\n", err)
		return exitErr
	}

	entries, err := loadEntries(files, projectDir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "load error: %v\n", err)
		return exitErr
	}

	deduped := aggregate.Dedup(entries)
	emitUnknownModelWarnings(deduped, stderr)
	reports := aggregate.Aggregate(deduped)
	for i := range reports {
		reports[i].ProjectPath = absCwd
	}

	enc := json.NewEncoder(stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(reports); err != nil {
		fmt.Fprintf(stderr, "encode error: %v\n", err)
		return exitErr
	}
	return exitOK
}

// resolveExplicitConfigDirs returns nil when neither the flag nor the
// env var is set (signaling "use defaults"). When the flag is set it
// takes precedence over the env var (full replace, not merge).
func resolveExplicitConfigDirs(flagVal, envVal string) []string {
	src := flagVal
	if src == "" {
		src = envVal
	}
	if src == "" {
		return nil
	}
	parts := strings.Split(src, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func loadEntries(files []string, projectDir string, stderr *os.File) ([]aggregate.FileEntry, error) {
	debug := os.Getenv("DEBUG") == "1"
	var out []aggregate.FileEntry
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sid := transcript.DeriveSessionID(path, projectDir)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			data := sc.Bytes()
			if len(data) == 0 {
				continue
			}
			e, perr := transcript.ParseLine(data)
			if perr != nil {
				if debug {
					fmt.Fprintf(stderr, "WARN: skip %s:%d reason=invalid-json\n", path, lineNo)
				}
				continue
			}
			if e == nil {
				continue
			}
			out = append(out, aggregate.FileEntry{
				FilePath:   path,
				LineNumber: lineNo,
				SessionID:  sid,
				Entry:      *e,
			})
		}
		if err := sc.Err(); err != nil {
			f.Close()
			return nil, fmt.Errorf("scan %s: %w", path, err)
		}
		f.Close()
	}
	// Files came from CollectJSONLFiles which already sorts; preserve
	// that order for downstream determinism.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		return out[i].LineNumber < out[j].LineNumber
	})
	return out, nil
}

func emitUnknownModelWarnings(entries []aggregate.FileEntry, stderr *os.File) {
	for _, occ := range aggregate.CollectUnknownModels(entries) {
		fmt.Fprintf(stderr, "WARN: unknown model %q (sessionId=%s)\n", occ.Model, occ.SessionID)
	}
}
