package codereview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupCLIPathFindsCommonUserInstallDirs(t *testing.T) {
	home := t.TempDir()
	npmBin := filepath.Join(home, ".npm-global", "bin")
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.12.0", "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(npmBin, "claude")
	codexPath := filepath.Join(nvmBin, "codex")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	gotClaude, ok := lookupCLIPath("claude")
	if !ok || gotClaude != claudePath {
		t.Fatalf("lookupCLIPath(claude) = (%q, %v), want (%q, true)", gotClaude, ok, claudePath)
	}
	gotCodex, ok := lookupCLIPath("codex")
	if !ok || gotCodex != codexPath {
		t.Fatalf("lookupCLIPath(codex) = (%q, %v), want (%q, true)", gotCodex, ok, codexPath)
	}
}
