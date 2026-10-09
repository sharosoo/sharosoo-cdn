package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRewriteText(t *testing.T) {
	cases := map[string]string{
		"![a](https://cdn.jsdelivr.net/gh/sharosoo/image@main/goa2/a.png)":           "![a](https://cdn.sharosoo.com/goa2/a.png)",
		"https://cdn.jsdelivr.net/gh/sharosoo/image@354d750/x/y.webp?v=2":            "https://cdn.sharosoo.com/x/y.webp?v=2",
		"https://raw.githubusercontent.com/sharosoo/image/main/k/fig.png":            "https://cdn.sharosoo.com/k/fig.png",
		"https://raw.githubusercontent.com/sharosoo/image/refs/heads/main/k/fig.png": "https://cdn.sharosoo.com/k/fig.png",
		"https://github.com/sharosoo/image/blob/main/cc.png":                         "https://cdn.sharosoo.com/cc.png",
		// Other repos and npm packages on jsDelivr are not ours to move.
		"https://cdn.jsdelivr.net/gh/sharosoo/fonts@v1/a.woff2": "https://cdn.jsdelivr.net/gh/sharosoo/fonts@v1/a.woff2",
		"https://cdn.jsdelivr.net/npm/mermaid@10/dist/m.js":     "https://cdn.jsdelivr.net/npm/mermaid@10/dist/m.js",
	}
	for in, want := range cases {
		if got, _ := rewriteText(in); got != want {
			t.Errorf("rewriteText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeKeyRejectsTraversalAndDirs(t *testing.T) {
	for _, k := range []string{"", "/", "a/", "a//b", "../x", "a/./b", "a/../b"} {
		if _, err := normalizeKey(k); err == nil {
			t.Errorf("normalizeKey(%q) accepted", k)
		}
	}
	if k, err := normalizeKey("/goa2/a.png"); err != nil || k != "goa2/a.png" {
		t.Errorf("normalizeKey(/goa2/a.png) = %q, %v", k, err)
	}
}

func TestCollectDirectoryKeepsRelativePathsAndSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.png", "sub/b.svg", ".git/HEAD", "sub/.DS_Store"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	items, err := collect([]string{dir}, "/topic/", "")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range items {
		keys = append(keys, it.key)
	}
	if want := []string{"topic/a.png", "topic/sub/b.svg"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if _, err := collect([]string{filepath.Join(dir, "a.png"), filepath.Join(dir, "sub", "b.svg")}, "", "x.png"); err == nil {
		t.Error("--key with two files accepted")
	}
}
