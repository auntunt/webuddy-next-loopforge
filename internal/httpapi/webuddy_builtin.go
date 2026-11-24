package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow/internal/builtinagent"

	"github.com/gin-gonic/gin"
)

// GET /api/webuddy/builtin-agent/status
func (h *webuddyEnvHandlers) builtinStatus(c *gin.Context) {
	st := builtinagent.Default().Status(c.Request.Context())
	c.JSON(http.StatusOK, st)
}

// PUT /api/webuddy/builtin-agent/config
func (h *webuddyEnvHandlers) builtinSaveConfig(c *gin.Context) {
	var input builtinagent.CustomConfig
	if !decode(c, &input) {
		return
	}
	if err := builtinagent.Default().SaveCustomConfig(input); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	st := builtinagent.Default().Status(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": st})
}

// POST /api/webuddy/builtin-agent/chat
// Fast zero-config built-in Agent endpoint (optionally grounded in a repo's .tmh KB).
func (h *webuddyKBHandlers) builtinChat(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Prompt   string `json:"prompt"`
	}
	if !decode(c, &input) {
		return
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt 不能为空"})
		return
	}
	evidence := ""
	if rp := strings.TrimSpace(input.RepoPath); rp != "" {
		if raw, err := h.svc.Chain(c.Request.Context(), rp, prompt, 3); err == nil {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				evidence, _ = m["markdown"].(string)
			}
		}
	}
	if evidence != "" {
		qa := synthesizeKBAnswer(c.Request.Context(), "chain", prompt, evidence)
		c.JSON(http.StatusOK, qa)
		return
	}
	res := builtinagent.Default().Complete(c.Request.Context(), builtinagent.CompletionRequest{
		SystemPrompt: "你是 Webuddy 内置助手。请用不超过 3 句大白话直接给出清晰、可执行的回答，禁止废话和客套。",
		UserPrompt:   prompt,
		MaxTokens:    350,
	})
	c.JSON(http.StatusOK, gin.H{
		"aiAnswer":    res.Text,
		"aiModel":     res.Model,
		"aiProvider":  res.Provider,
		"aiLatencyMs": res.LatencyMs,
		"aiError":     res.Error,
	})
}
