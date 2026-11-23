package target

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileName is the project file: committed to a repository, it says which
// organization and project commands run anywhere inside it use.
const FileName = ".collector.toml"

// FindFile looks for the project file in dir and each directory above
// it, and reads the nearest. With none, path is "".
func FindFile(dir string) (g Given, path string, err error) {
	if dir == "" {
		return Given{}, "", nil
	}
	for {
		p := filepath.Join(dir, FileName)
		b, err := os.ReadFile(p) //nolint:gosec // G304: the project file, looked for by name above the working directory.
		switch {
		case err == nil:
			g, err := parseFile(string(b))
			if err != nil {
				return Given{}, p, fmt.Errorf("%s: %w", p, err)
			}
			return g, p, nil
		case !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission):
			return Given{}, p, fmt.Errorf("reading %s: %w", p, err)
		}
		up := filepath.Dir(dir)
		if up == dir {
			return Given{}, "", nil
		}
		dir = up
	}
}

// WriteFile writes the project file in dir.
func WriteFile(dir string, g Given) error {
	var b strings.Builder
	b.WriteString("# The Collector organization and project for commands run in this\n")
	b.WriteString("# repository. Written by collector use --here; commit it.\n")
	fmt.Fprintf(&b, "org = %s\n", strconv.Quote(g.Org))
	if g.Project != "" {
		fmt.Fprintf(&b, "project = %s\n", strconv.Quote(g.Project))
	}
	return os.WriteFile(filepath.Join(dir, FileName), []byte(b.String()), 0o644) //nolint:gosec // Committed, not secret.
}

// RemoveFile removes the project file in dir; false if there was none.
func RemoveFile(dir string) (bool, error) {
	err := os.Remove(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// parseFile reads the little TOML the project file needs: comments, and
// org and project as strings.
func parseFile(text string) (Given, error) {
	var g Given
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok {
			return Given{}, fmt.Errorf("line %d: expected key = \"value\"", n)
		}
		if key != "org" && key != "project" {
			return Given{}, fmt.Errorf("line %d: unknown key %q (use org and project)", n, key)
		}
		if seen[key] {
			return Given{}, fmt.Errorf("line %d: %s is set twice", n, key)
		}
		seen[key] = true
		val, err := tomlString(strings.TrimSpace(rest))
		if err != nil {
			return Given{}, fmt.Errorf("line %d: %s: %w", n, key, err)
		}
		if strings.TrimSpace(val) == "" {
			return Given{}, fmt.Errorf("line %d: %s is empty", n, key)
		}
		if key == "org" {
			g.Org = val
		} else {
			g.Project = val
		}
	}
	if g.Org == "" {
		return Given{}, errors.New("no org (a project needs its org)")
	}
	return g, nil
}

// tomlString reads a quoted string, then allows only a comment after it.
func tomlString(s string) (string, error) {
	if s == "" || (s[0] != '"' && s[0] != '\'') {
		return "", errors.New("expected a quoted string")
	}
	q := s[0]
	end := -1
	for i := 1; i < len(s); i++ {
		if q == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			end = i
			break
		}
	}
	if end < 0 {
		return "", errors.New("unterminated string")
	}
	if tail := strings.TrimSpace(s[end+1:]); tail != "" && !strings.HasPrefix(tail, "#") {
		return "", errors.New("unexpected text after the string")
	}
	if q == '\'' {
		return s[1:end], nil
	}
	return strconv.Unquote(s[:end+1])
}
