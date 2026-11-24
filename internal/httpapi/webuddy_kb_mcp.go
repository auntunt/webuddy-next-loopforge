package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// POST /api/webuddy/kb/mcp
//
// Stateless Streamable HTTP MCP server exposing Webuddy's built-in Code Graph,
// Project Knowledge Base, Built-in Domain Knowledge, and Source Evidence
// Verifier directly to LoopForge SOP Agents, Main Bot, and external coding CLIs.
func (h *webuddyKBHandlers) mcp(c *gin.Context) {
	if c.Request.Method != http.MethodPost {
		c.Header("Allow", "POST")
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "Streamable HTTP MCP accepts JSON-RPC POST requests"})
		return
	}
	var req agentToolRPCRequest
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, agentToolRPCResponse{
			JSONRPC: "2.0",
			Error:   &agentToolRPCError{Code: -32700, Message: "parse error", Data: err.Error()},
		})
		return
	}
	resp := h.handleMCPRPC(c.Request.Context(), req)
	if resp == nil {
		c.Status(http.StatusAccepted)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *webuddyKBHandlers) handleMCPRPC(ctx context.Context, req agentToolRPCRequest) *agentToolRPCResponse {
	if req.Method == "notifications/initialized" || req.Method == "notifications/cancelled" {
		return nil
	}
	if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
		return nil
	}
	switch req.Method {
	case "initialize":
		return &agentToolRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "webuddy-kb", "version": "2.0.0"},
			},
		}
	case "ping":
		return &agentToolRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &agentToolRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": webuddyKBToolSpecs(),
			},
		}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &agentToolRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &agentToolRPCError{Code: -32602, Message: "invalid params", Data: err.Error()},
			}
		}
		raw, err := h.dispatchMCPTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return &agentToolRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"isError": true,
					"content": []map[string]any{{"type": "text", "text": err.Error()}},
				},
			}
		}
		return &agentToolRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": string(raw)}},
			},
		}
	default:
		return &agentToolRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &agentToolRPCError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (h *webuddyKBHandlers) dispatchMCPTool(ctx context.Context, name string, args map[string]any) ([]byte, error) {
	strArg := func(k string) string {
		if v, ok := args[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	intArg := func(k string) int {
		switch v := args[k].(type) {
		case json.Number:
			n, _ := v.Int64()
			return int(n)
		case float64:
			return int(v)
		default:
			return 0
		}
	}
	switch name {
	case "kb_search":
		return h.svc.Search(ctx, strArg("repoPath"), strArg("query"), intArg("limit"))
	case "kb_fetch":
		return h.svc.ReadFile(ctx, strArg("repoPath"), strArg("path"), intArg("start"), intArg("end"))
	case "kb_overview":
		return h.svc.Overview(ctx, strArg("repoPath"))
	case "kb_verify_evidence":
		return h.svc.VerifyEvidence(ctx, strArg("repoPath"), strArg("ref"))
	case "kb_impact":
		return h.svc.Impact(ctx, strArg("repoPath"), strArg("target"), intArg("depth"))
	case "kb_chain":
		return h.svc.Chain(ctx, strArg("repoPath"), strArg("query"), intArg("flows"))
	default:
		return nil, &agentToolUnknownError{Name: name}
	}
}

type agentToolUnknownError struct{ Name string }

func (e *agentToolUnknownError) Error() string { return "unknown webuddy kb tool: " + e.Name }

func webuddyKBToolSpecs() []map[string]any {
	return []map[string]any{
		{
			"name":        "kb_search",
			"description": "检索 Webuddy 代码图谱（业务域、流程、数据表）、项目知识页（.tmh/knowledge）与内置领域知识（国密、国产数据库、信创等）。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":    map[string]any{"type": "string", "description": "搜索关键词"},
					"repoPath": map[string]any{"type": "string", "description": "可选：本地代码仓库路径"},
					"limit":    map[string]any{"type": "integer", "description": "最大返回条数，默认 30"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "kb_fetch",
			"description": "按行号读取内置领域知识（builtin:xxx.md）或仓库内知识页/源码文件。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"type": "string", "description": "文档路径，如 builtin:crypto-sm.md 或 .tmh/knowledge/..."},
					"repoPath": map[string]any{"type": "string", "description": "读取仓库内文件时必填"},
					"start":    map[string]any{"type": "integer", "description": "起始行号（1-based）"},
					"end":      map[string]any{"type": "integer", "description": "结束行号（1-based）"},
				},
				"required": []string{"path"},
			},
		},
		{
			"name":        "kb_overview",
			"description": "获取项目代码图谱总览（业务域、业务流程、数据表 DDL/外键 ER 关系、CRUD 矩阵及内置知识清单）。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repoPath": map[string]any{"type": "string", "description": "可选：本地代码仓库路径"},
				},
			},
		},
		{
			"name":        "kb_verify_evidence",
			"description": "防幻觉核验源码引用（格式：文件:行号 或 文件:起行-止行），确认文件存在且行号未越界并返回真实代码片段。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repoPath": map[string]any{"type": "string", "description": "本地代码仓库路径"},
					"ref":      map[string]any{"type": "string", "description": "源码引用，例如 internal/httpapi/server.go:1-20"},
				},
				"required": []string{"repoPath", "ref"},
			},
		},
		{
			"name":        "kb_impact",
			"description": "变更影响面分析：输入文件路径、符号、表名或接口关键词，返回受影响的调用链、业务域、业务流程与数据表。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repoPath": map[string]any{"type": "string", "description": "本地代码仓库路径"},
					"target":   map[string]any{"type": "string", "description": "要分析影响的文件/符号/表名/接口"},
					"depth":    map[string]any{"type": "integer", "description": "调用链深度，默认 3"},
				},
				"required": []string{"repoPath", "target"},
			},
		},
		{
			"name":        "kb_chain",
			"description": "业务链路证据问答：输入业务需求或问题，自动追溯关联业务域、流程、实体与可核验源码证据链。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repoPath": map[string]any{"type": "string", "description": "本地代码仓库路径"},
					"query":    map[string]any{"type": "string", "description": "业务问题或需求描述"},
					"flows":    map[string]any{"type": "integer", "description": "最多追踪流程数，默认 6"},
				},
				"required": []string{"repoPath", "query"},
			},
		},
	}
}
