package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"agentflow/internal/domain"
)

func TestUnknownHTTPSRepositoryNeedsNoHostingAdapter(t *testing.T) {
	f := cvBNewHTTPFixture(t)
	resp := cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]string{"projectId": "platform", "repoUrl": "https://unfamiliar.example.org/team/code.git"}, true)
	var r domain.Repository
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &r) != nil || r.Provider != domain.RepositoryProviderHTTPS || r.CloneURL != "https://unfamiliar.example.org/team/code.git" || r.GongfengProjectID != "" {
		t.Fatalf("generic HTTPS repository: HTTP %d %s", resp.Code, resp.Body.String())
	}
	for _, address := range []string{"https://user:token@unfamiliar.example.org/team/code", "https://unfamiliar.example.org/team/code?token=x", "https://unfamiliar.example.org/team/../code"} {
		if response := cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]string{"projectId": "platform", "repoUrl": address}, true); response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe URL accepted: %s", address)
		}
	}
	// Self-hosted LAN/HTTP GitLab repositories are accepted when loose project onboarding is enabled.
	respHTTP := cvBRequest(t, f, http.MethodPost, "/api/repositories", map[string]string{"projectId": "platform", "repoUrl": "http://127.0.0.1:8929/root/private-demo.git"}, true)
	var rHTTP domain.Repository
	if respHTTP.Code != http.StatusOK || json.Unmarshal(respHTTP.Body.Bytes(), &rHTTP) != nil || rHTTP.Provider != domain.RepositoryProviderHTTPS || rHTTP.CloneURL != "http://127.0.0.1:8929/root/private-demo.git" || rHTTP.Name != "private-demo" {
		t.Fatalf("self-hosted HTTP repository: HTTP %d %s", respHTTP.Code, respHTTP.Body.String())
	}

	// Verify unified diff extraction from repository-patch summary+diff format.
	sampleArtifact := "repository-patch summary\nfiles 1  +2/-0\nM\tREADME.md\n\n--- unified diff ---\ndiff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1,3 @@\n # Demo\n+\n+Added by SOP\n... (diff truncated at 64KB; inspect workspace for full diff)\n"
	extracted := extractUnifiedDiffFromArtifactContent(sampleArtifact)
	if extracted == "" || extracted[:11] != "diff --git " {
		t.Fatalf("extractUnifiedDiffFromArtifactContent failed: %q", extracted)
	}
}
