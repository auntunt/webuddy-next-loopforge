package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/webuddykb"
)

var remoteMirrorLocks sync.Map

func mirrorMutex(key string) *sync.Mutex {
	v, _ := remoteMirrorLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func remoteMirrorDirForRepo(r domain.Repository) string {
	if strings.TrimSpace(r.LocalPath) != "" {
		return ""
	}
	cu := strings.TrimSpace(r.CloneURL)
	if !strings.HasPrefix(strings.ToLower(cu), "https://") && !strings.HasPrefix(strings.ToLower(cu), "http://") {
		return ""
	}
	slug := sanitizeRepoSlug(r.ID)
	if slug == "" {
		slug = sanitizeRepoSlug(r.Name)
	}
	if slug == "" {
		return ""
	}
	return filepath.Join(resolveUploadedReposDir(), "remote-"+slug)
}

func isSyntheticOrLocalLoopbackHost(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return true
	}
	h := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "git.example.com" || h == "unfamiliar.example.org" {
		return true
	}
	for _, suf := range []string{".example.com", ".example.org", ".example.net", ".invalid", ".test"} {
		if strings.HasSuffix(h, suf) || h == strings.TrimPrefix(suf, ".") {
			return true
		}
	}
	return false
}

func shouldAutoMirrorRemoteRepo(r domain.Repository) bool {
	if strings.TrimSpace(r.LocalPath) != "" {
		return false
	}
	cu := strings.TrimSpace(r.CloneURL)
	if cu == "" || isSyntheticOrLocalLoopbackHost(cu) {
		return false
	}
	return remoteMirrorDirForRepo(r) != ""
}

// resolveGitTokenForHost looks up an HTTP Git token for host across candidate user IDs
// without leaking credentials across unrelated tenants.
func (d *deps) resolveGitCredForHost(ctx context.Context, host string, candidateUIDs ...int64) (username, token string) {
	if d == nil || d.repo == nil {
		return "", ""
	}
	h := strings.ToLower(strings.TrimSpace(host))
	seen := map[int64]bool{}
	for _, uid := range candidateUIDs {
		if uid <= 0 || seen[uid] {
			continue
		}
		seen[uid] = true
		if cred, ok := d.repo.GitHTTPCredentialForHost(ctx, uid, h); ok && strings.TrimSpace(cred.Token) != "" {
			user := strings.TrimSpace(cred.GitUsername())
			if user == "" {
				user = "x-access-token"
			}
			return user, strings.TrimSpace(cred.Token)
		}
		if h == "github.com" {
			if u, ok := d.repo.User(uid); ok && strings.TrimSpace(u.GitToken) != "" {
				return "x-access-token", strings.TrimSpace(u.GitToken)
			}
		}
	}
	return "", ""
}

// gitAuthEnvForURL constructs environment variables passing a scoped HTTP Basic Authorization
// header via GIT_CONFIG_COUNT/KEY/VALUE so tokens never appear in argv or .git/config.
func gitAuthEnvForURL(rawURL, username, token string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if strings.TrimSpace(token) == "" {
		return env
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return env
	}
	if strings.TrimSpace(username) == "" {
		username = "x-access-token"
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	scopeKey := "http." + strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + "/.extraHeader"
	env = append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0="+scopeKey,
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+b64,
	)
	return env
}

// effectiveKBRepoPath returns r.LocalPath if set, or the local mirror path for remote HTTP(S)
// repositories (materializing the shallow mirror on demand if ensureClone is true).
func (d *deps) effectiveKBRepoPath(ctx context.Context, r domain.Repository, actorUID int64, ensureClone bool, refreshExisting bool) string {
	if lp := strings.TrimRight(strings.TrimSpace(r.LocalPath), "/"); lp != "" {
		return lp
	}
	mirrorDir := remoteMirrorDirForRepo(r)
	if mirrorDir == "" {
		return ""
	}
	if ex, _, _, ig := inspectLocalRepo(mirrorDir); ex && ig {
		if refreshExisting && shouldAutoMirrorRemoteRepo(r) {
			d.refreshRemoteRepoMirror(ctx, r, mirrorDir, actorUID)
		}
		return mirrorDir
	}
	if !ensureClone || !shouldAutoMirrorRemoteRepo(r) {
		return ""
	}
	if ok := d.cloneRemoteRepoMirror(ctx, r, mirrorDir, actorUID); ok {
		return mirrorDir
	}
	return ""
}

func (d *deps) repoCandidateUIDs(r domain.Repository, actorUID int64) []int64 {
	uids := []int64{actorUID}
	if d != nil && d.repo != nil && r.ProjectID != "" {
		if p, ok := d.repo.Project(r.ProjectID); ok && p.OwnerID > 0 {
			uids = append(uids, p.OwnerID)
		}
	}
	return uids
}

func (d *deps) cloneRemoteRepoMirror(ctx context.Context, r domain.Repository, mirrorDir string, actorUID int64) bool {
	mu := mirrorMutex(mirrorDir)
	mu.Lock()
	defer mu.Unlock()

	if ex, _, _, ig := inspectLocalRepo(mirrorDir); ex && ig {
		return true
	}
	if err := os.MkdirAll(filepath.Dir(mirrorDir), 0o755); err != nil {
		return false
	}
	_ = os.RemoveAll(mirrorDir)

	u, err := url.Parse(strings.TrimSpace(r.CloneURL))
	if err != nil {
		return false
	}
	user, tok := d.resolveGitCredForHost(ctx, u.Host, d.repoCandidateUIDs(r, actorUID)...)
	env := gitAuthEnvForURL(r.CloneURL, user, tok)

	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Clone default branch shallowly first (resilient whether remote uses main or master).
	cmd := exec.CommandContext(cctx, "git", "clone", "--depth", "1", "--no-single-branch", strings.TrimSpace(r.CloneURL), mirrorDir)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(mirrorDir)
		return false
	}
	return true
}

func (d *deps) refreshRemoteRepoMirror(ctx context.Context, r domain.Repository, mirrorDir string, actorUID int64) {
	mu := mirrorMutex(mirrorDir)
	mu.Lock()
	defer mu.Unlock()

	u, err := url.Parse(strings.TrimSpace(r.CloneURL))
	if err != nil {
		return
	}
	user, tok := d.resolveGitCredForHost(ctx, u.Host, d.repoCandidateUIDs(r, actorUID)...)
	env := gitAuthEnvForURL(r.CloneURL, user, tok)

	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	fetchCmd := exec.CommandContext(cctx, "git", "-C", mirrorDir, "fetch", "--depth", "1", "origin")
	fetchCmd.Env = env
	if err := fetchCmd.Run(); err == nil {
		resetCmd := exec.CommandContext(cctx, "git", "-C", mirrorDir, "reset", "--hard", "FETCH_HEAD")
		resetCmd.Env = env
		_ = resetCmd.Run()
	}
}

func (d *deps) ensureRemoteRepoMirrorAndScan(r domain.Repository, actorUID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	lp := d.effectiveKBRepoPath(ctx, r, actorUID, true, false)
	if lp == "" {
		return
	}
	_, hg, hkb, _ := inspectLocalRepo(lp)
	if !hg || !hkb {
		svc := webuddykb.NewService()
		_, _ = svc.Scan(ctx, lp)
	}
}
