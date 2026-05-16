package transcript

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoProjectDir is returned by ResolveProjectDir when neither the encoded
// path nor any cwd-fallback scan locates the project.
var ErrNoProjectDir = errors.New("no project dir")

// ErrNoValidConfigDir is returned by EnumerateDataRoots when the caller
// supplied explicit config directories but none of them contain a
// "projects" subdirectory.
var ErrNoValidConfigDir = errors.New("no valid claude config dir")

// EncodeCwd converts an absolute cwd to the corresponding Claude project
// directory name. Both "/" and "." are replaced with "-", matching the
// observed Claude Code encoding (e.g. "/Users/foo.bar/x" becomes
// "-Users-foo-bar-x").
func EncodeCwd(cwd string) string {
	r := strings.NewReplacer("/", "-", ".", "-")
	return r.Replace(cwd)
}

// EnumerateOptions selects which Claude config directories to scan.
// ExplicitConfigDirs, when non-nil, comes from --claude-config-dir or
// CLAUDE_CONFIG_DIR. Otherwise defaults derived from XDGConfigHome and
// HomeDir are tried.
type EnumerateOptions struct {
	ExplicitConfigDirs []string
	XDGConfigHome      string
	HomeDir            string
}

// EnumerateDataRoots returns the list of <root> paths whose <root>/projects
// directory exists. When ExplicitConfigDirs is set but yields no valid
// root, ErrNoValidConfigDir is returned. Defaults silently drop missing
// roots.
func EnumerateDataRoots(opts EnumerateOptions) ([]string, error) {
	if opts.ExplicitConfigDirs != nil {
		valid := filterRootsWithProjects(opts.ExplicitConfigDirs)
		if len(valid) == 0 {
			return nil, ErrNoValidConfigDir
		}
		return valid, nil
	}
	xdg := opts.XDGConfigHome
	if xdg == "" {
		xdg = filepath.Join(opts.HomeDir, ".config")
	}
	candidates := []string{
		filepath.Join(xdg, "claude"),
		filepath.Join(opts.HomeDir, ".claude"),
	}
	return filterRootsWithProjects(candidates), nil
}

func filterRootsWithProjects(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		info, err := os.Stat(filepath.Join(p, "projects"))
		if err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// ResolveProjectDir returns the project directory under one of the roots
// that corresponds to cwd. First the encoded name is tried; if no
// matching directory exists, every project directory's JSONL contents are
// scanned for a top-level "cwd" field that equals the requested cwd.
func ResolveProjectDir(roots []string, cwd string) (string, error) {
	encoded := EncodeCwd(cwd)
	for _, root := range roots {
		candidate := filepath.Join(root, "projects", encoded)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	for _, root := range roots {
		projectsDir := filepath.Join(root, "projects")
		entries, err := os.ReadDir(projectsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			projectDir := filepath.Join(projectsDir, e.Name())
			if matchesCwd(projectDir, cwd) {
				return projectDir, nil
			}
		}
	}
	return "", fmt.Errorf("%w for cwd: %s", ErrNoProjectDir, cwd)
}

func matchesCwd(projectDir, cwd string) bool {
	files, err := CollectJSONLFiles(projectDir)
	if err != nil {
		return false
	}
	for _, f := range files {
		if fileContainsCwd(f, cwd) {
			return true
		}
	}
	return false
}

func fileContainsCwd(path, cwd string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		got, ok := ExtractCwdField(line)
		if ok && got == cwd {
			return true
		}
	}
	return false
}

// CollectJSONLFiles walks the directory and returns every *.jsonl file
// path. Paths are sorted to provide deterministic iteration for callers.
func CollectJSONLFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// DeriveSessionID returns the session id for a JSONL file path located
// under the given project directory. Nested form (file is two or more
// levels deep) uses the parent directory name; flat form (file directly
// under projectDir) uses the basename without extension.
func DeriveSessionID(filePath, projectDir string) string {
	rel, err := filepath.Rel(projectDir, filePath)
	if err != nil {
		return strings.TrimSuffix(filepath.Base(filePath), ".jsonl")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 1 {
		return strings.TrimSuffix(parts[0], ".jsonl")
	}
	return parts[len(parts)-2]
}
