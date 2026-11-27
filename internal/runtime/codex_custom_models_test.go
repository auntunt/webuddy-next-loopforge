package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCustomCodexGatewayIgnoresConflictingAuthJSONAndDiscoversModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-test-aiseat-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6-luna"},{"id":"gpt-6-astra"},{"id":"gpt-6.2-sol"},{"id":"gpt-image-1"}]}`))
	}))
	defer srv.Close()

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cfg := `model_provider = "aiseat"
model = "gpt-6-luna"
model_reasoning_effort = "medium"

[model_providers.aiseat]
name = "aiseat"
base_url = "` + srv.URL + `/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-test-aiseat-token"
`
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate leftover ChatGPT OAuth auth.json that previously hijacked WSL2 onto ChatGPT account mode!
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"tokens":{"access_token":"chatgpt-oauth-stale","refresh_token":"rt-stale"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stageRoot := t.TempDir()
	spec, ok := runnerLoginSpec("codex")
	if !ok {
		t.Fatal("missing codex runnerLoginSpec")
	}
	cleanup, _, err := prepareConversationRunnerLogin(stageRoot, spec, true)
	if err != nil {
		t.Fatalf("prepareConversationRunnerLogin failed: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	stagedCodex := filepath.Join(stageRoot, "home", ".codex")
	if _, err := os.Stat(filepath.Join(stagedCodex, "config.toml")); err != nil {
		t.Fatalf("expected staged config.toml, got err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagedCodex, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("expected conflicting auth.json NOT to be staged when custom gateway is configured!")
	}

	models, err := DiscoverRunnerLoginModels(context.Background(), "codex", "codex")
	if err != nil {
		t.Fatalf("DiscoverRunnerLoginModels failed: %v", err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	want := []string{"gpt-6-luna", "gpt-6-astra", "gpt-6.2-sol"}
	if !slices.Equal(ids, want) {
		t.Fatalf("got models %v, want %v", ids, want)
	}
}
