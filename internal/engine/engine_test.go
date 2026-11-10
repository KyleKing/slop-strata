package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/kyleking/slop-strata/internal/cache"
	"github.com/kyleking/slop-strata/internal/config"
	"github.com/kyleking/slop-strata/internal/engine"
	"github.com/kyleking/slop-strata/internal/rules"
)

const sleepSrc = "package p\n\nimport \"time\"\n\nfunc wait() { time.Sleep(1) }\n"

func TestSameBlobUnderOtherPathClassKeepsItsFindings(t *testing.T) {
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
	eng := engine.New(config.Default(), store)
	const sha = "0123456789abcdef"

	test := eng.Analyze(ctx, "pkg/wait_test.go", sha, []byte(sleepSrc))
	source := eng.Analyze(ctx, "pkg/wait.go", sha, []byte(sleepSrc))

	if got := countRule(test.Findings, "go/timing-hack"); got != 1 {
		t.Errorf("test path go/timing-hack = %d, want 1", got)
	}
	if got := countRule(source.Findings, "go/timing-hack"); got != 0 {
		t.Errorf("source path go/timing-hack = %d, want 0", got)
	}
}

func countRule(fs []rules.Finding, rule string) int {
	n := 0
	for _, f := range fs {
		if f.Rule == rule {
			n++
		}
	}

	return n
}
