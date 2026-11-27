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

func TestHandleRunnerCLIConfigGatewaySyncAndSwitchBackToOfficialLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6-luna"},{"id":"gpt-6-sol"}]}`))
		case "/v1/responses":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: response.created\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("[projects.\"/home/wsl\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-official"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{}
	resp := d.handleRunnerCLIConfig(context.Background(), &domain.RunnerCLIConfigRequest{
		Provider:             "codex",
		Action:               "set",
		GatewayName:          "team_gw",
		BaseURL:              srv.URL + "/v1",
		APIKey:               "sk-super-secret-1234",
		Model:                "gpt-6-sol",
		Effort:               "high",
		ExtraProviderTOML:    `http_headers = { "x-org" = "demo" }`,
		ClearConflictingAuth: true,
	})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var st domain.RunnerCLIConfigStatus
	if err := json.Unmarshal(resp.Payload, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.AuthSource != "custom_gateway" || st.GatewayName != "team_gw" || st.Model != "gpt-6-sol" || st.HasOAuthFile || st.ClearedAuthBackup == "" {
		t.Fatalf("unexpected CLI config status: %+v", st)
	}
	if strings.Contains(st.TokenMasked, "super-secret") {
		t.Fatalf("token leaked in masked field: %s", st.TokenMasked)
	}
	written, err := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `[projects."/home/wsl"]`) || !strings.Contains(string(written), `x-org`) {
		t.Fatalf("expected existing [projects.*] and generic extra provider TOML to be preserved, got:\n%s", string(written))
	}

	// Switch back to official login: restores backed-up auth.json and strips custom gateway while keeping [projects.*]
	respOfficial := d.handleRunnerCLIConfig(context.Background(), &domain.RunnerCLIConfigRequest{
		Provider:         "codex",
		Action:           "set",
		UseOfficialLogin: true,
	})
	if respOfficial.Error != "" {
		t.Fatalf("switch to official login failed: %s", respOfficial.Error)
	}
	var stOff domain.RunnerCLIConfigStatus
	if err := json.Unmarshal(respOfficial.Payload, &stOff); err != nil {
		t.Fatal(err)
	}
	if stOff.AuthSource != "official" || stOff.HasCustomAuth || !stOff.HasOAuthFile {
		t.Fatalf("expected official OAuth login restored, got: %+v", stOff)
	}
	afterOff, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if strings.Contains(string(afterOff), "experimental_bearer_token") || !strings.Contains(string(afterOff), `[projects."/home/wsl"]`) {
		t.Fatalf("expected custom gateway stripped and [projects.*] preserved, got:\n%s", string(afterOff))
	}
}
