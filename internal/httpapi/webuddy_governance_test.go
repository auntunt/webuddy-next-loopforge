package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUploadedReposDir(t *testing.T) {
	t.Run("prefers explicit WEBUDDY_UPLOADED_REPOS_DIR", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "custom-uploaded-repos")
		t.Setenv("WEBUDDY_UPLOADED_REPOS_DIR", custom)
		t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
		if got := resolveUploadedReposDir(); got != custom {
			t.Fatalf("resolveUploadedReposDir() = %q, want %q", got, custom)
		}
	})

	t.Run("falls back to HOME without writing under writable cwd", func(t *testing.T) {
		t.Setenv("WEBUDDY_UPLOADED_REPOS_DIR", "")
		home := filepath.Join(t.TempDir(), "home")
		t.Setenv("HOME", home)
		cwd := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(origWd) })

		got := resolveUploadedReposDir()
		want := filepath.Join(home, ".webuddy-uploaded-repos")
		if got != want {
			t.Fatalf("resolveUploadedReposDir() = %q, want %q", got, want)
		}
		if _, err := os.Stat(filepath.Join(cwd, "uploaded-repos")); !os.IsNotExist(err) {
			t.Fatalf("resolveUploadedReposDir() must not create uploaded-repos in cwd %q", cwd)
		}
	})
}
