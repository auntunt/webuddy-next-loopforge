package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/domain"

	"github.com/gin-gonic/gin"
)

func TestWorkbenchRunnerCLIConfigAndZeroTokenSlashCommands(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	hostTOML := `model_provider = "aiseat"
model = "gpt-6-luna"
model_reasoning_effort = "medium"

[model_providers.aiseat]
name = "aiseat"
base_url = "https://ai.cloudwaveai.cn/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-host-secret-9999"
`
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(hostTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	h, owner := workbenchFixture(t)
	hub := quietRunnerHub()
	h.deps.runnerControls = hub
	runnersH := &runnersHandlers{deps: h.deps, runnerControls: hub, runnerBindCodes: map[string]runnerBindCode{}}
	h.deps.runners = runnersH

	wslRunner := domain.Runner{
		ID:            "runner-wsl2",
		Name:          "Windows WSL2 测试机",
		OwnerUserID:   owner.ID,
		OS:            "linux",
		Arch:          "amd64",
		Status:        domain.RunnerStatusOnline,
		RunnerVersion: domain.RunnerVersionCLISettings,
		Runtimes:      []domain.RunnerRuntime{{Provider: "codex", AuthMode: domain.RuntimeAuthLocal}},
	}
	if err := h.repo.PutRunner(wslRunner); err != nil {
		t.Fatal(err)
	}
	conn := testRunnerConn(wslRunner.ID)
	registerTestConn(hub, conn)
	t.Cleanup(conn.close)

	// 1. Test PUT /api/runners/:id/cli-config with syncFromHost=true
	c, w := workbenchContext(owner, http.MethodPut, "/api/runners/runner-wsl2/cli-config", bytes.NewBufferString(`{"provider":"codex","syncFromHost":true}`))
	c.Params = gin.Params{{Key: "id", Value: wslRunner.ID}}
	done := make(chan struct{})
	go func() {
		runnersH.runnerCLIConfig(c)
		close(done)
	}()
	rpcReq := answerOnce(t, hub, conn, domain.RunnerWorkspaceResponse{
		Status: 200,
		Payload: []byte(`{"provider":"codex","exists":true,"gatewayName":"aiseat","baseUrl":"https://ai.cloudwaveai.cn/v1","tokenMasked":"sk-h****9999","model":"gpt-6-luna","hasCustomAuth":true,"discoveredModels":["gpt-6-luna","gpt-6.2-sol"]}`),
	})
	<-done
	if w.Code != http.StatusOK {
		t.Fatalf("runnerCLIConfig returned %d: %s", w.Code, w.Body.String())
	}
	if rpcReq.Operation != domain.RunnerWorkspaceOperationCLIConfig || rpcReq.CLIConfig == nil ||
		rpcReq.CLIConfig.GatewayName != "aiseat" || rpcReq.CLIConfig.APIKey != "sk-host-secret-9999" || !rpcReq.CLIConfig.ClearConflictingAuth {
		t.Fatalf("unexpected CLI config RPC payload: %+v", rpcReq.CLIConfig)
	}

	// 2. Test Zero-Token Slash Commands (/runner, /runner add, /runner use) via h.chat without ANY model API key configured!
	chatSlash := func(cmd string) assistantReply {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"sessionId": "slash-session-1",
			"messages":  []map[string]any{{"role": "user", "content": cmd}},
		})
		ctx, rec := workbenchContext(owner, http.MethodPost, "/api/assistant/chat", bytes.NewBuffer(body))
		h.chat(ctx)
		if rec.Code != http.StatusOK {
			t.Fatalf("slash command %q failed with %d: %s", cmd, rec.Code, rec.Body.String())
		}
		var reply assistantReply
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		return reply
	}

	listReply := chatSlash("/runner")
	if !strings.Contains(listReply.Message, "Windows WSL2 测试机") || !strings.Contains(listReply.Message, "0 Token") {
		t.Fatalf("unexpected /runner output: %s", listReply.Message)
	}

	addReply := chatSlash("/runner add")
	if !strings.Contains(addReply.Message, "/runners/install.sh") || !strings.Contains(addReply.Message, "bind-") {
		t.Fatalf("unexpected /runner add output: %s", addReply.Message)
	}

	useReply := chatSlash("/runner use WSL2")
	if !strings.Contains(useReply.Message, "已将默认执行机器切换为") || h.repo.DefaultRunner(owner.ID) != wslRunner.ID {
		t.Fatalf("unexpected /runner use output: %s", useReply.Message)
	}

	// Verify conversation engine is NOT locked by zero-token slash turns!
	saved, ok := h.repo.AssistantSession(owner.ID, "slash-session-1")
	if !ok || len(saved.Messages) != 2 {
		t.Fatalf("unexpected saved session: %+v", saved)
	}
	if locked := assistantConversationEngine(saved.Messages, saved.Executor); locked != "" {
		t.Fatalf("expected zero-token slash turn not to lock CLI engine, got %q", locked)
	}
	if saved.Messages[1].Usage == nil || saved.Messages[1].Usage.TotalTokens != 0 {
		t.Fatalf("expected 0 token usage on slash turn, got %+v", saved.Messages[1].Usage)
	}
}
