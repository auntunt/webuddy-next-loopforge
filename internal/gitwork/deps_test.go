package gitwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkLocalDependencyDirs(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "node_modules", "fflate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}

	LinkLocalDependencyDirs(src, dst)

	target, err := os.Readlink(filepath.Join(dst, "node_modules"))
	if err != nil {
		t.Fatalf("expected node_modules symlink: %v", err)
	}
	want, _ := filepath.Abs(filepath.Join(src, "node_modules"))
	if target != want {
		t.Fatalf("symlink target=%q want=%q", target, want)
	}
	exclude, err := os.ReadFile(filepath.Join(dst, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), "node_modules") {
		t.Fatalf("expected node_modules in .git/info/exclude, got %q err=%v", string(exclude), err)
	}
}
