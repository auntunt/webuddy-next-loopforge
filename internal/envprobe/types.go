package envprobe

import "time"

// HostProfile is a complete, sanitized snapshot of a machine's hardware,
// toolchains, coding CLIs (with optional live probe status), reachable Git
// remotes, discovered .tmh knowledge bases, and external MCP servers.
type HostProfile struct {
	Host           string            `json:"host"`
	OS             string            `json:"os"`
	Arch           string            `json:"arch"`
	ScannedAt      time.Time         `json:"scannedAt"`
	Hardware       HardwareInfo      `json:"hardware"`
	Toolchains     ToolchainInfo     `json:"toolchains"`
	CLIs           []CLIStatus       `json:"clis"`
	GitRemotes     []GitRemoteStatus `json:"gitRemotes"`
	KnowledgeBases []DiscoveredKB    `json:"knowledgeBases"`
	ExternalMCPs   []DiscoveredMCP   `json:"externalMcps"`
}

type HardwareInfo struct {
	CPUCores     int     `json:"cpuCores"`
	MemTotalMB   int64   `json:"memTotalMB"`
	MemAvailMB   int64   `json:"memAvailMB"`
	DiskTotalGB  float64 `json:"diskTotalGB"`
	DiskAvailGB  float64 `json:"diskAvailGB"`
	SandboxHint  string  `json:"sandboxHint"`  // "docker_ready" | "local_recommended"
	SandboxLabel string  `json:"sandboxLabel"` // human-readable Chinese guidance
}

type ToolStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type ToolchainInfo struct {
	Docker     ToolStatus `json:"docker"`
	Git        ToolStatus `json:"git"`
	Node       ToolStatus `json:"node"`
	Python     ToolStatus `json:"python"`
	Go         ToolStatus `json:"go"`
	Playwright ToolStatus `json:"playwright"`
}

type CLIStatus struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Binary       string    `json:"binary"`
	Available    bool      `json:"available"`
	Path         string    `json:"path,omitempty"`
	Version      string    `json:"version,omitempty"`
	AuthMode     string    `json:"authMode"` // "oauth" | "custom_gateway" | "env_key" | "none"
	AuthHint     string    `json:"authHint,omitempty"`
	GatewayURL   string    `json:"gatewayUrl,omitempty"`
	DefaultModel string    `json:"defaultModel,omitempty"`
	Models       []string  `json:"models,omitempty"`
	ProbeStatus  string    `json:"probeStatus"` // "untested" | "probing" | "ok" | "auth_error" | "model_error" | "error"
	ProbeError   string    `json:"probeError,omitempty"`
	ProbeLatency int64     `json:"probeLatencyMs,omitempty"`
	ProbedAt     time.Time `json:"probedAt,omitempty"`
}

type GitRemoteStatus struct {
	Host      string `json:"host"`
	URL       string `json:"url"`
	Source    string `json:"source"` // ".git-credentials" | "repo"
	Reachable *bool  `json:"reachable,omitempty"`
	Error     string `json:"error,omitempty"`
}

type DiscoveredKB struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	HasGraph        bool   `json:"hasGraph"`
	HasKB           bool   `json:"hasKb"`
	IsGit           bool   `json:"isGit"`
	GitRemote       string `json:"gitRemote,omitempty"`
	DefaultBranch   string `json:"defaultBranch,omitempty"`
	AlreadyImported bool   `json:"alreadyImported,omitempty"`
}

type DiscoveredMCP struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`    // "~/.codex/config.toml" | "~/.claude/settings.json"
	Transport string   `json:"transport"` // "http" | "stdio"
	Target    string   `json:"target"`    // URL or command
	Args      []string `json:"args,omitempty"`
}
