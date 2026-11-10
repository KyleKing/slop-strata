package cache_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/cache"
)

func TestStoreAnswersOnlyTheExactKey(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := cache.Open(ctx, filepath.Join(t.TempDir(), "blobs.db"))
	if err != nil {
		t.Fatalf("opening cache: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("closing cache: %v", err)
		}
	})
	payload := []byte(`{"metrics":{}}`)
	store.Put(ctx, "aaa", "go:source", "analysis-1", payload)

	cases := []struct {
		name     string
		sha      string
		class    string
		analysis string
		hit      bool
	}{
		{name: "same key", sha: "aaa", class: "go:source", analysis: "analysis-1", hit: true},
		{name: "other path class", sha: "aaa", class: "go:test", analysis: "analysis-1"},
		{name: "analysis from other code", sha: "aaa", class: "go:source", analysis: "analysis-2"},
		{name: "other blob", sha: "bbb", class: "go:source", analysis: "analysis-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := store.Get(ctx, tc.sha, tc.class, tc.analysis)
			if ok != tc.hit {
				t.Fatalf("Get hit = %t, want %t", ok, tc.hit)
			}
			if tc.hit && !bytes.Equal(got, payload) {
				t.Errorf("Get payload = %q, want %q", got, payload)
			}
		})
	}
}

func TestStoreKeepsEntriesAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "blobs.db")
	first, err := cache.Open(ctx, path)
	if err != nil {
		t.Fatalf("opening cache: %v", err)
	}
	first.Put(ctx, "aaa", "go:source", "analysis-1", []byte("kept"))
	if err := first.Close(); err != nil {
		t.Fatalf("closing cache: %v", err)
	}
	second, err := cache.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopening cache: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("closing cache: %v", err)
		}
	})
	if got, ok := second.Get(ctx, "aaa", "go:source", "analysis-1"); !ok || string(got) != "kept" {
		t.Fatalf("reopened Get = %q, %t; want the stored payload", got, ok)
	}
}

func TestPathNamesTheCacheFilePerToolVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version string
		file    string
	}{
		{version: "", file: "blobs-dev.db"},
		{version: "0.51.0", file: "blobs-0.51.0.db"},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			path, err := cache.Path(tc.version)
			if err != nil {
				t.Fatalf("Path(%q): %v", tc.version, err)
			}
			if filepath.Base(path) != tc.file || !strings.Contains(path, "slop-strata") {
				t.Errorf("Path(%q) = %q, want slop-strata/%s", tc.version, path, tc.file)
			}
		})
	}
}
