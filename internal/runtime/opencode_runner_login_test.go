package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOpenCodeRunnerLoginCommandUsesMachineModel(t *testing.T) {
	local := AgentCLICommand(AgentCLIOpenCode, AgentCLICommandOptions{Binary: "opencode", Model: "opencode/big-pickle", Prompt: "hi", RunnerLogin: true})
	if !slices.Contains(local.Argv, "opencode/big-pickle") {
		t.Fatalf("local login argv = %v", local.Argv)
	}
	keyed := AgentCLICommand(AgentCLIOpenCode, AgentCLICommandOptions{Binary: "opencode", Model: "gpt-5", Prompt: "hi"})
	if !slices.Contains(keyed.Argv, "agentflow/gpt-5") {
		t.Fatalf("API-key argv = %v", keyed.Argv)
	}
}

func TestOpenCodeRunnerLoginCopiesOptionalAuth(t *testing.T) {
	host := t.TempDir()
	t.Setenv("HOME", host)
	login, ok := runnerLoginSpec(AgentCLIOpenCode)
	if !ok {
		t.Fatal("OpenCode has no runner login")
	}
	home := AgentCLIHome{Base: t.TempDir(), Rel: "home"}
	if err := os.MkdirAll(home.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	// Account-free models work without auth.json.
	cleanup, _, err := prepareRunnerLogin(home, login, true)
	if err != nil || cleanup != nil {
		t.Fatalf("missing auth.json: cleanup set = %v, err = %v", cleanup != nil, err)
	}
	source := filepath.Join(host, login.dir)
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, login.file), []byte(`{"openai":{"type":"api","key":"k"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup, _, err = prepareRunnerLogin(home, login, true)
	if err != nil || cleanup == nil {
		t.Fatalf("prepare: cleanup set = %v, err = %v", cleanup != nil, err)
	}
	copied := filepath.Join(home.Path(), login.dir, login.file)
	if data, err := os.ReadFile(copied); err != nil || !strings.Contains(string(data), `"openai"`) {
		t.Fatalf("copied login = %q, %v", data, err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("login copy survived cleanup: %v", err)
	}
}

func TestOpenCodeLoginEnvironmentLoadsSystemPrompt(t *testing.T) {
	env := openCodeLoginEnvironment("/ws/agent-system.md")
	if !strings.Contains(env["OPENCODE_CONFIG_CONTENT"], `"instructions":["/ws/agent-system.md"]`) || strings.Contains(env["OPENCODE_CONFIG_CONTENT"], "agentflow") {
		t.Fatalf("config = %s", env["OPENCODE_CONFIG_CONTENT"])
	}
}
