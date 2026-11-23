package sourcemaps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const plainScript = "import { Controller } from \"@hotwired/stimulus\"\n" +
	"\n" +
	"export default class extends Controller {\n" +
	"  connect() {\n" +
	"    this.element.textContent = \"Hello\" // é, 😀\n" +
	"    boom()\n" +
	"  }\n" +
	"}\n"

// --sources makes a map for each script that has none: line for line, and
// column for column at the start of every token, carrying the script.
func TestFindSourcesMakesMaps(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"assets/controllers/hello-1a2b3c4d.js": plainScript,
		"assets/app-9f8e7d6c.js":               "x\n//# sourceMappingURL=app-9f8e7d6c.js.map",
		"assets/app-9f8e7d6c.js.map":           testMap("app-9f8e7d6c.js", nil),
		"assets/inline.js":                     "x\n//# sourceMappingURL=data:application/json;base64,e30=",
		"assets/styles.css":                    "body {}\n",
	})
	found, err := Find(dir, FindOptions{URLPrefix: "/", Sources: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Warnings) != 0 {
		t.Errorf("warnings = %q", found.Warnings)
	}
	var paths []string
	for _, m := range found.Maps {
		paths = append(paths, m.Path)
	}
	// The bundler's map stays; the inline one isn't replaced; CSS isn't a script.
	if want := []string{"/assets/app-9f8e7d6c.js", "/assets/controllers/hello-1a2b3c4d.js"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	if found.Maps[0].Generated {
		t.Error("the bundler's map counted as generated")
	}
	m := found.Maps[1]
	if !m.Generated || m.MapFile != "" || m.BundleFile != filepath.Join(dir, "assets", "controllers", "hello-1a2b3c4d.js") ||
		m.Bytes != int64(len(m.Content)) {
		t.Fatalf("generated map = %+v", m)
	}
	var sm struct {
		Version        int      `json:"version"`
		File           string   `json:"file"`
		Sources        []string `json:"sources"`
		SourcesContent []string `json:"sourcesContent"`
		Names          []string `json:"names"`
		Mappings       string   `json:"mappings"`
	}
	if err := json.Unmarshal(m.Content, &sm); err != nil {
		t.Fatal(err)
	}
	if sm.Version != 3 || sm.File != "hello-1a2b3c4d.js" || !slices.Equal(sm.Sources, []string{"assets/controllers/hello.js"}) ||
		len(sm.SourcesContent) != 1 || sm.SourcesContent[0] != plainScript || sm.Names == nil {
		t.Errorf("map = %+v", sm)
	}
	lines := decodeMappings(t, sm.Mappings)
	// Every generated position maps to itself, at each token's start: a
	// run of identifier characters, or any other character but a space.
	want := [][]int{
		{0, 7, 9, 20, 22, 27, 28, 29, 37, 38, 46},
		nil,
		{0, 7, 15, 21, 29, 40},
		{2, 9, 10, 12},
		{4, 8, 9, 16, 17, 29, 31, 32, 37, 39, 40, 42, 43, 45}, // Columns count UTF-16 units, as browsers do.
		{4, 8, 9},
		{2},
		{0},
	}
	if len(lines) < len(want) {
		t.Fatalf("%d lines of mappings, want %d", len(lines), len(want))
	}
	for i, cols := range want {
		var got []int
		for _, s := range lines[i] {
			if s.source != 0 || s.line != i || s.srcCol != s.col {
				t.Errorf("line %d: segment %+v isn't the identity", i+1, s)
			}
			got = append(got, s.col)
		}
		if !slices.Equal(got, cols) {
			t.Errorf("line %d: columns %v, want %v", i+1, got, cols)
		}
	}
}

// Minified scripts can't be mapped line for line usefully: skipped, with a
// warning.
func TestFindSourcesSkipsMinified(t *testing.T) {
	long := "var a=1;" + strings.Repeat("function f(a){return a+1};", 40) + "\n"
	dir := writeTree(t, map[string]string{
		"vendor/lib.min.js": "var a = 1\n",
		"vendor/packed.js":  long + long,
		"app.js":            "let a = 1\n" + strings.Repeat("x", 600) + "\nlet b = 2\nlet c = 3\nlet d = 4\nlet e = 5\nlet f = 6\nlet g = 7\nlet h = 8\nlet i = 9\nlet j = 10\nlet k = 11\nlet l = 12\nlet m = 13\nlet n = 14\nlet o = 15\nlet p = 16\nlet q = 17\nlet r = 18\nlet s = 19\nlet t = 20\n",
	})
	found, err := Find(dir, FindOptions{URLPrefix: "/", Sources: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Maps) != 1 || found.Maps[0].Path != "/app.js" {
		t.Errorf("maps = %+v, want only app.js (one long line isn't minified)", found.Maps)
	}
	want := []string{
		"vendor/lib.min.js: skipped, it looks minified: build a source map for it instead",
		"vendor/packed.js: skipped, it looks minified: build a source map for it instead",
	}
	if !slices.Equal(found.Warnings, want) {
		t.Errorf("warnings = %q\nwant %q", found.Warnings, want)
	}
}

func TestFindSourcesSizeLimit(t *testing.T) {
	dir := writeTree(t, map[string]string{"big.js": strings.Repeat("let a = 1\n", 200)})
	found, err := Find(dir, FindOptions{URLPrefix: "/", Sources: true, MaxMapBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Maps) != 0 || len(found.Warnings) != 1 || !strings.Contains(found.Warnings[0], "big.js: skipped, its map would be over") {
		t.Errorf("maps = %+v, warnings = %q", found.Maps, found.Warnings)
	}
}

// Without --sources, scripts without maps are left alone.
func TestFindWithoutSources(t *testing.T) {
	if maps := mapsIn(t, map[string]string{"app.js": plainScript}); len(maps) != 0 {
		t.Errorf("maps = %+v", maps)
	}
}

// --delete deletes the bundler's maps, never the scripts behind generated ones.
func TestDeleteLeavesGeneratedMaps(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app.js":     plainScript,
		"lib.js":     "x\n//# sourceMappingURL=lib.js.map",
		"lib.js.map": testMap("lib.js", nil),
	})
	found, err := Find(dir, FindOptions{URLPrefix: "/", Sources: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := Delete(found.Maps); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "app.js")); err != nil || string(b) != plainScript {
		t.Errorf("app.js = %q, %v", b, err)
	}
	if exists(filepath.Join(dir, "lib.js.map")) {
		t.Error("lib.js.map is still there")
	}
}

type segment struct{ col, source, line, srcCol int }

// decodeMappings decodes a v3 mappings string into lines of segments with
// absolute values.
func decodeMappings(t *testing.T, mappings string) [][]segment {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out [][]segment
	source, line, srcCol := 0, 0, 0
	for _, l := range strings.Split(mappings, ";") {
		var segs []segment
		col := 0
		for _, s := range strings.Split(l, ",") {
			if s == "" {
				continue
			}
			var vals []int
			v, shift := 0, 0
			for _, c := range s {
				d := strings.IndexRune(alphabet, c)
				if d < 0 {
					t.Fatalf("bad VLQ %q", s)
				}
				v += (d & 31) << shift
				if d&32 != 0 {
					shift += 5
					continue
				}
				n := v >> 1
				if v&1 == 1 {
					n = -n
				}
				vals = append(vals, n)
				v, shift = 0, 0
			}
			if len(vals) != 4 {
				t.Fatalf("segment %q has %d fields", s, len(vals))
			}
			col += vals[0]
			source += vals[1]
			line += vals[2]
			srcCol += vals[3]
			segs = append(segs, segment{col, source, line, srcCol})
		}
		out = append(out, segs)
	}
	return out
}

// A generated map goes up like any other, its part named after the script.
func TestUploadSendsGeneratedMaps(t *testing.T) {
	srv := startServer(t, nil)
	found, err := Find(writeTree(t, map[string]string{"js/app.js": plainScript}), FindOptions{URLPrefix: "/assets", Sources: true})
	if err != nil {
		t.Fatal(err)
	}
	res := Upload(context.Background(), found.Maps, Options{Release: "r1", Host: srv.URL, Token: testToken, HTTPClient: srv.Client()})
	reqs := srv.received()
	if !res.OK || len(reqs) != 1 || len(reqs[0].Manifest) != 1 {
		t.Fatalf("result = %+v, requests = %+v", res, reqs)
	}
	if e := reqs[0].Manifest[0]; e.Path != "/assets/js/app.js" || e.Map != "app.js.map" {
		t.Errorf("manifest = %+v", reqs[0].Manifest)
	}
	if m, ok := reqs[0].Parts[0].JSON.(map[string]any); !ok || m["sourcesContent"].([]any)[0] != plainScript {
		t.Errorf("sent %+v", reqs[0].Parts[0].JSON)
	}
}

// The asset digest leaves the source's name, so frames keep one name
// across deploys; the map's file keeps it, as the browser reports it.
func TestUndigested(t *testing.T) {
	for in, want := range map[string]string{
		"controllers/hello-1a2b3c4d.js": "controllers/hello.js",
		"application-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.js": "application.js",
		"app-da39a3ee5e6b4b0d3255bfef95601890afd80709.mjs":                                "app.mjs",
		"app-deadbeef.js":    "app-deadbeef.js", // No digit: a word, maybe.
		"app-1a2b3c4.js":     "app-1a2b3c4.js",  // 7: not a digest length.
		"app-1A2B3C4D.js":    "app-1A2B3C4D.js",
		"lib/-1a2b3c4d.js":   "lib/-1a2b3c4d.js",
		"date-picker.js":     "date-picker.js",
		"v-20261123.js":      "v-20261123.js", // A date.
		"hello-1a2b3c4d.css": "hello-1a2b3c4d.css",
	} {
		if got := undigested(in); got != want {
			t.Errorf("undigested(%q) = %q, want %q", in, got, want)
		}
	}
}
