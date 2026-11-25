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
	if authRequired() && !h.requirePlatformAdmin(c) {
		return
	}
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
	kbErr := ""
	if rawRP := strings.TrimSpace(input.RepoPath); rawRP != "" {
		rp, ok := h.resolveAuthorizedKBRepo(c, rawRP, false)
		if !ok {
			return
		}
		raw, err := h.svc.Chain(c.Request.Context(), rp, prompt, 3)
		if err != nil {
			kbErr = err.Error()
		} else {
			var m map[string]any
			if jerr := json.Unmarshal(raw, &m); jerr != nil {
				kbErr = jerr.Error()
			} else {
				evidence, _ = m["markdown"].(string)
			}
		}
	}
	if evidence != "" {
		qa := synthesizeKBAnswer(c.Request.Context(), "chain", prompt, evidence)
		c.JSON(http.StatusOK, gin.H{
			"aiAnswer":    qa.Answer,
			"aiCitations": qa.Citations,
			"aiModel":     qa.Model,
			"aiProvider":  qa.Provider,
			"aiLatencyMs": qa.LatencyMs,
			"aiError":     qa.Error,
			"kbGrounded":  true,
		})
		return
	}
	res := builtinagent.Default().Complete(c.Request.Context(), builtinagent.CompletionRequest{
		SystemPrompt: "你是 Webuddy 内置助手。请用不超过 3 句大白话直接给出清晰、可执行的回答，禁止废话和客套。",
		UserPrompt:   prompt,
		MaxTokens:    500,
	})
	c.JSON(http.StatusOK, gin.H{
		"aiAnswer":    res.Text,
		"aiModel":     res.Model,
		"aiProvider":  res.Provider,
		"aiLatencyMs": res.LatencyMs,
		"aiError":     res.Error,
		"kbGrounded":  false,
		"kbError":     kbErr,
	})
}
