package gitwork

import (
	"os"
	"path/filepath"
	"strings"
)

// localDependencyDirs lists common pre-installed dependency caches that are
// gitignored in source checkouts and cannot be re-downloaded inside offline
// execution sandboxes.
var localDependencyDirs = []string{"node_modules", ".venv", "venv", "vendor"}

// LinkLocalDependencyDirs symlinks pre-installed dependency directories (such as
// node_modules) from a local source repository into a freshly cloned workspace
// so offline/sandboxed test runners can execute tests immediately without
// running `npm ci`. Each linked directory is also added to .git/info/exclude so
// `git status` and repository patches stay clean.
func LinkLocalDependencyDirs(sourceDir, targetDir string) {
	sourceDir = strings.TrimSpace(sourceDir)
	targetDir = strings.TrimSpace(targetDir)
	if sourceDir == "" || targetDir == "" {
		return
	}
	srcInfo, err := os.Stat(sourceDir)
	if err != nil || !srcInfo.IsDir() {
		return
	}
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil || absSource == absTarget {
		return
	}
	var linked []string
	for _, name := range localDependencyDirs {
		src := filepath.Join(absSource, name)
		info, err := os.Stat(src)
		if err != nil || !info.IsDir() {
			continue
		}
		dst := filepath.Join(absTarget, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.Symlink(src, dst); err == nil {
			linked = append(linked, name)
		}
	}
	if len(linked) == 0 {
		return
	}
	excludePath := filepath.Join(absTarget, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return
	}
	existing, _ := os.ReadFile(excludePath)
	content := string(existing)
	var toAdd []string
	for _, name := range linked {
		if !strings.Contains(content, "\n"+name+"\n") && !strings.HasPrefix(content, name+"\n") && strings.TrimSpace(content) != name {
			toAdd = append(toAdd, name)
		}
	}
	if len(toAdd) == 0 {
		return
	}
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(toAdd, "\n") + "\n"
	_ = os.WriteFile(excludePath, []byte(content), 0o644)
}
