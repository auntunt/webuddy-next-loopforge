package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/domain"
)

func TestHandleRunnerCLIConfigSyncAndClearConflictingAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6-luna"},{"id":"gpt-6.2-sol"}]}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Existing config with [projects.*] and conflicting auth.json
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("[projects.\"/home/wsl\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"tokens":{"access_token":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{}
	resp := d.handleRunnerCLIConfig(context.Background(), &domain.RunnerCLIConfigRequest{
		Provider:             "codex",
		Action:               "set",
		GatewayName:          "aiseat",
		BaseURL:              srv.URL + "/v1",
		APIKey:               "sk-super-secret-1234",
		Model:                "gpt-6.2-sol",
		Effort:               "high",
		ClearConflictingAuth: true,
	})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var st domain.RunnerCLIConfigStatus
	if err := json.Unmarshal(resp.Payload, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.GatewayName != "aiseat" || st.Model != "gpt-6.2-sol" || st.HasOAuthFile || st.ClearedAuthBackup == "" {
		t.Fatalf("unexpected CLI config status: %+v", st)
	}
	if strings.Contains(st.TokenMasked, "super-secret") {
		t.Fatalf("token leaked in masked field: %s", st.TokenMasked)
	}
	written, err := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `[projects."/home/wsl"]`) {
		t.Fatalf("expected existing [projects.*] section to be preserved, got:\n%s", string(written))
	}
}
