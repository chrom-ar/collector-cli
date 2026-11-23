package sourcemaps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const minifiedHint = "skipped, it looks minified: build a source map for it instead"

// generated makes the map of a script served as written: each position
// maps to itself, at the start of every token, and the map carries the
// script as its source. It returns nil for a script to skip, with the
// warning to give (if any).
func generated(root, script string, maxBytes int64) (m *Map, warning string, err error) {
	content, err := os.ReadFile(script) //nolint:gosec // G304: a script found under the directory the user named.
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", script, err)
	}
	lines := splitLines(string(content))
	if minified(filepath.Base(script), lines) {
		return nil, minifiedHint, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err = enc.Encode(struct {
		Version        int      `json:"version"`
		File           string   `json:"file"`
		Sources        []string `json:"sources"`
		SourcesContent []string `json:"sourcesContent"`
		Names          []string `json:"names"`
		Mappings       string   `json:"mappings"`
	}{3, filepath.Base(script), []string{undigested(filepath.ToSlash(relativeTo(root, script)))}, []string{string(content)}, []string{}, identityMappings(lines)})
	if err != nil {
		return nil, "", fmt.Errorf("making the map of %s: %w", script, err)
	}
	data := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if int64(len(data)) > maxBytes {
		return nil, fmt.Sprintf("skipped, its map would be over %s", size(maxBytes)), nil
	}
	return &Map{Path: script, BundleFile: script, Bytes: int64(len(data)), Content: data, Generated: true}, "", nil
}

// assetDigest is the fingerprint an asset pipeline adds before the
// extension: 8, 32, 40 or 64 hex digits.
var assetDigest = regexp.MustCompile(`-([0-9a-f]{64}|[0-9a-f]{40}|[0-9a-f]{32}|[0-9a-f]{8})(\.[cm]?js)$`)

// undigested is the script's path without its asset digest, so frames
// keep one name across deploys: controllers/hello-1a2b3c4d.js is
// controllers/hello.js. Only hex with both digits and letters counts, so
// a date (-20261123) or a word (-deadbeef) stays.
func undigested(path string) string {
	m := assetDigest.FindStringSubmatchIndex(path)
	if m == nil || m[0] == 0 || strings.HasSuffix(path[:m[0]], "/") {
		return path
	}
	if hex := path[m[2]:m[3]]; !strings.ContainsAny(hex, "0123456789") || !strings.ContainsAny(hex, "abcdef") {
		return path
	}
	return path[:m[0]] + path[m[4]:m[5]]
}

func size(n int64) string {
	if n >= 1024*1024 {
		return fmt.Sprintf("%.0f MB", float64(n)/1024/1024)
	}
	return fmt.Sprintf("%d bytes", n)
}

// splitLines splits at JavaScript's line terminators: \n, \r\n, \r,
// U+2028 and U+2029.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\r' && strings.HasPrefix(s[i+1:], "\n"):
			lines, start, i = append(lines, s[start:i]), i+2, i+2
		case r == '\n' || r == '\r' || r == ' ' || r == ' ':
			lines, start, i = append(lines, s[start:i]), i+n, i+n
		default:
			i += n
		}
	}
	return append(lines, s[start:])
}

// minified: by name, or long lines on average with one very long.
func minified(name string, lines []string) bool {
	if strings.Contains(name, ".min.") {
		return true
	}
	total, longest := 0, 0
	for _, l := range lines {
		total += len(l)
		longest = max(longest, len(l))
	}
	return longest > 500 && total/len(lines) > 100
}

// identityMappings maps every token start to itself: a run of identifier
// characters (ASCII letters, digits, _ and $, or anything non-ASCII), or
// any other character but white space. Columns count UTF-16 units, as
// browsers report them.
func identityMappings(lines []string) string {
	var b strings.Builder
	prevLine, prevCol := 0, 0 // The source position carries over lines.
	for i, line := range lines {
		if i > 0 {
			b.WriteByte(';')
		}
		col, genCol, inWord, first := 0, 0, false, true
		for _, r := range line {
			word := r >= utf8.RuneSelf || r == '_' || r == '$' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			space := r == ' ' || r == '\t' || r == '\v' || r == '\f' || r == 0xa0 || r == 0xfeff
			if !space && (!word || !inWord) {
				if !first {
					b.WriteByte(',')
				}
				vlq(&b, col-genCol)
				vlq(&b, 0)
				vlq(&b, i-prevLine)
				vlq(&b, col-prevCol)
				genCol, prevLine, prevCol, first = col, i, col, false
			}
			inWord = word && !space
			col += utf16.RuneLen(r)
		}
	}
	return b.String()
}

const vlqDigits = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func vlq(b *strings.Builder, n int) {
	v := n << 1
	if n < 0 {
		v = (-n << 1) | 1
	}
	for {
		d := v & 31
		v >>= 5
		if v > 0 {
			d |= 32
		}
		b.WriteByte(vlqDigits[d])
		if v == 0 {
			return
		}
	}
}
