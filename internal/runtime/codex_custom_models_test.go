package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCustomCodexGatewayFiltersUnaccessibleModelsAndIgnoresStaleAuthJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer sk-test-gateway-token" || r.Header.Get("x-custom-actor") != "team-alpha" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6-luna"},{"id":"gpt-6-sol"},{"id":"fusion"},{"id":"gpt-6.1-terra"},{"id":"gpt-6.1-sol"},{"id":"gpt-image-1"}]}`))
		case "/v1/responses":
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(body, &req)
			switch req.Model {
			case "gpt-6.1-terra":
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"message":"auth_unavailable: no auth available (last upstream error: model_not_found)"}}`))
			case "gpt-6.1-sol":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account."}}`))
			default:
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("event: response.created\n\n"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cfg := `model_provider = "generic_gw"
model = "gpt-6-luna"
model_reasoning_effort = "medium"

[model_providers.generic_gw]
name = "generic_gw"
base_url = "` + srv.URL + `/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-test-gateway-token"
http_headers = { "x-custom-actor" = "team-alpha" }
`
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
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
	want := []string{"gpt-6-luna", "gpt-6-sol", "fusion"}
	if !slices.Equal(ids, want) {
		t.Fatalf("expected unaccessible models (gpt-6.1-terra, gpt-6.1-sol) to be filtered out; got %v, want %v", ids, want)
	}
}

func TestOfficialCodexOAuthLoginWorksNativelyWithoutGatewayCoupling(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	// Normal official config.toml (only project trust, no custom bearer token) + official auth.json
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-5.3-codex\"\n\n[projects.\"/Users/dev/app\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-official-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stageRoot := t.TempDir()
	spec, _ := runnerLoginSpec("codex")
	cleanup, _, err := prepareConversationRunnerLogin(stageRoot, spec, true)
	if err != nil {
		t.Fatalf("official login prepareConversationRunnerLogin failed: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	stagedCodex := filepath.Join(stageRoot, "home", ".codex")
	if _, err := os.Stat(filepath.Join(stagedCodex, "auth.json")); err != nil {
		t.Fatalf("expected official auth.json to be staged for normal login: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagedCodex, "config.toml")); err != nil {
		t.Fatalf("expected official config.toml to be staged alongside auth.json: %v", err)
	}
}
