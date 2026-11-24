package envprobe

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseCodexConfig(t *testing.T) {
	toml := `
model = "gpt-6-luna"
model_provider = "aiseat"

[model_providers.aiseat]
name = "aiseat"
base_url = "https://ai.cloudwaveai.cn/v1"
experimental_bearer_token = "sk-super-secret-token-12345"
requires_openai_auth = false
supports_websockets = false

[mcp_servers.webuddy_kb]
url = "https://user:pass@webuddy.bbstack.date/api/webuddy/kb/mcp"

[mcp_servers.local_tool]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
`
	cfg := parseCodexConfig(toml)
	if cfg.DefaultModel != "gpt-6-luna" {
		t.Fatalf("expected default model gpt-6-luna, got %q", cfg.DefaultModel)
	}
	if cfg.GatewayURL != "https://ai.cloudwaveai.cn/v1" {
		t.Fatalf("expected gateway url, got %q", cfg.GatewayURL)
	}
	if !cfg.HasAuth {
		t.Fatalf("expected HasAuth=true")
	}
	if len(cfg.MCPs) != 2 {
		t.Fatalf("expected 2 MCPs, got %d: %+v", len(cfg.MCPs), cfg.MCPs)
	}
	if cfg.MCPs[0].Name != "webuddy_kb" || cfg.MCPs[0].Transport != "http" || cfg.MCPs[0].Target != "https://webuddy.bbstack.date/api/webuddy/kb/mcp" {
		t.Fatalf("unexpected first MCP (credentials must be stripped): %+v", cfg.MCPs[0])
	}
	if cfg.MCPs[1].Name != "local_tool" || cfg.MCPs[1].Transport != "stdio" || cfg.MCPs[1].Target != "npx" || len(cfg.MCPs[1].Args) != 3 {
		t.Fatalf("unexpected second MCP: %+v", cfg.MCPs[1])
	}
	raw, _ := json.Marshal(cfg.MCPs)
	if strings.Contains(string(raw), "sk-super-secret") || strings.Contains(string(raw), "user:pass") {
		t.Fatalf("secret leaked into MCP output: %s", string(raw))
	}
}

func TestParseClaudeSettings(t *testing.T) {
	raw := []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://zjz-ai.webtrn.cn",
			"ANTHROPIC_AUTH_TOKEN": "cr_secret_token_99999",
			"ANTHROPIC_MODEL": "claude-sonnet-4-5"
		},
		"mcpServers": {
			"demo-http": { "url": "https://tok:sec@example.com/mcp" },
			"demo-stdio": { "command": "uvx", "args": ["mcp-server-git"] }
		}
	}`)
	cfg := parseClaudeSettings(raw, "~/.claude/settings.json")
	if !cfg.HasAuth {
		t.Fatalf("expected HasAuth=true")
	}
	if cfg.GatewayURL != "https://zjz-ai.webtrn.cn" {
		t.Fatalf("unexpected gateway URL: %q", cfg.GatewayURL)
	}
	if cfg.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("unexpected default model: %q", cfg.DefaultModel)
	}
	if len(cfg.MCPs) != 2 {
		t.Fatalf("expected 2 MCPs, got %d", len(cfg.MCPs))
	}
	if cfg.MCPs[0].Target != "https://example.com/mcp" {
		t.Fatalf("expected sanitized URL, got %q", cfg.MCPs[0].Target)
	}
	encoded, _ := json.Marshal(cfg.MCPs)
	if strings.Contains(string(encoded), "cr_secret_token") || strings.Contains(string(encoded), "tok:sec") {
		t.Fatalf("secret leaked into Claude MCP output: %s", string(encoded))
	}
}

func TestParseGitCredentialsSanitized(t *testing.T) {
	creds := `
http://root:glpat-my-secret-password@127.0.0.1:8929
https://oauth2:ghp_anothersecret123@github.com
http://root:differentpass@127.0.0.1:8929
`
	remotes := parseGitCredentials(creds)
	if len(remotes) != 2 {
		t.Fatalf("expected 2 deduplicated remotes, got %d: %+v", len(remotes), remotes)
	}
	encoded, _ := json.Marshal(remotes)
	s := string(encoded)
	for _, secret := range []string{"glpat-my-secret", "ghp_anothersecret", "differentpass", "root:", "oauth2:"} {
		if strings.Contains(s, secret) {
			t.Fatalf("secret %q leaked in git remotes: %s", secret, s)
		}
	}
}

func TestClassifyAndSanitizeProbeError(t *testing.T) {
	msg := sanitizeProbeError("API Error: 403 {\"error\":{\"message\":\"用户已被封禁\"}} Bearer sk-abc12345", nil)
	if strings.Contains(msg, "sk-abc12345") {
		t.Fatalf("token was not redacted: %s", msg)
	}
	if got := classifyProbeError(msg); got != "auth_error" {
		t.Fatalf("expected auth_error, got %q", got)
	}
	if got := classifyProbeError("The model `gpt-6.1-sol` does not exist or model_not_found"); got != "model_error" {
		t.Fatalf("expected model_error, got %q", got)
	}
}

func TestScanHostBasic(t *testing.T) {
	prof := ScanHost(context.Background())
	if prof.OS == "" || prof.Arch == "" {
		t.Fatalf("expected OS/Arch populated: %+v", prof)
	}
	if prof.Hardware.CPUCores <= 0 {
		t.Fatalf("expected CPUCores > 0, got %d", prof.Hardware.CPUCores)
	}
	if prof.Hardware.SandboxHint == "" {
		t.Fatalf("expected SandboxHint populated")
	}
	if len(prof.CLIs) < 5 {
		t.Fatalf("expected at least 5 known CLIs scanned, got %d", len(prof.CLIs))
	}
}
