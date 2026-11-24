package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentflow/internal/envprobe"
)

type kbQAResult struct {
	Answer    string   `json:"aiAnswer,omitempty"`
	Citations []string `json:"aiCitations,omitempty"`
	Model     string   `json:"aiModel,omitempty"`
	LatencyMs int64    `json:"aiLatencyMs,omitempty"`
	Error     string   `json:"aiError,omitempty"`
}

type localLLMConfig struct {
	BaseURL     string
	BearerToken string
	Model       string
}

func loadLocalLLMConfig() localLLMConfig {
	home, _ := os.UserHomeDir()
	if home != "" {
		if raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
			var cfg localLLMConfig
			var section string
			for _, rawLine := range strings.Split(string(raw), "\n") {
				line := strings.TrimSpace(rawLine)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
					section = strings.TrimSpace(line[1 : len(line)-1])
					continue
				}
				eq := strings.IndexByte(line, '=')
				if eq < 0 {
					continue
				}
				key := strings.TrimSpace(line[:eq])
				val := strings.TrimSpace(line[eq+1:])
				if idx := strings.IndexByte(val, '#'); idx >= 0 && !strings.Contains(val[:idx], "://") {
					val = strings.TrimSpace(val[:idx])
				}
				val = strings.Trim(val, `"'`)
				if section == "" && key == "model" && val != "" {
					cfg.Model = val
				} else if strings.HasPrefix(section, "model_providers.") {
					if key == "base_url" && cfg.BaseURL == "" {
						cfg.BaseURL = strings.TrimRight(val, "/")
					} else if key == "experimental_bearer_token" && cfg.BearerToken == "" {
						cfg.BearerToken = val
					}
				}
			}
			if cfg.BaseURL != "" && cfg.BearerToken != "" {
				if cfg.Model == "" {
					cfg.Model = "gpt-6-luna"
				}
				return cfg
			}
		}
	}
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), "/")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
		if model == "" {
			model = "gpt-4o-mini"
		}
		return localLLMConfig{BaseURL: base, BearerToken: key, Model: model}
	}
	return localLLMConfig{}
}

func synthesizeKBAnswer(ctx context.Context, mode, question, evidence string) kbQAResult {
	start := time.Now()
	question = strings.TrimSpace(question)
	evidence = strings.TrimSpace(evidence)
	if question == "" {
		return kbQAResult{}
	}
	if len(evidence) > 10000 {
		evidence = evidence[:10000] + "\n...(后续证据已截断)"
	}

	var sysPrompt, userPrompt string
	if mode == "impact" {
		sysPrompt = "你是代码变更影响分析助手。严格根据提供的图谱分析结果，用不超过3句大白话（总字数≤100字）总结改动这里会影响哪些核心业务、风险大不大、最需要重点测什么。禁止废话和客套，禁止编造未出现的内容。"
		userPrompt = fmt.Sprintf("准备修改目标：%s\n\n图谱影响分析结果：\n%s", question, evidence)
	} else {
		sysPrompt = "你是代码库业务问答助手。严格根据下面提供的「知识库检索证据」回答用户问题：\n" +
			"1. 用不超过 3 句大白话直接回答（总字数 ≤ 120 字），禁止客套、禁止抄写文档元信息（id/status/commit）。\n" +
			"2. 如果证据里有对应的源码位置（格式为 路径:行号），最后另起一行写最多 3 个最关键的位置，格式：引用: path/A.java:42, path/B.py:18\n" +
			"3. 如果证据里确实不包含该问题的答案，只回复一句：「当前知识库暂未记录该逻辑，建议换个更具体的模块/接口词或在工作台让 AI 查源码。」绝不硬凑或编造。"
		userPrompt = fmt.Sprintf("用户问题：%s\n\n知识库检索证据：\n%s", question, evidence)
	}

	cfg := loadLocalLLMConfig()
	tctx, cancel := context.WithTimeout(ctx, 22*time.Second)
	defer cancel()

	var rawText string
	var err error
	modelUsed := cfg.Model

	if cfg.BaseURL != "" && cfg.BearerToken != "" {
		rawText, err = callResponsesOrChatHTTP(tctx, cfg, sysPrompt, userPrompt)
	}
	if strings.TrimSpace(rawText) == "" {
		// Fallback to codex exec CLI if direct HTTP didn't succeed
		if cliText, cliErr := callCodexExecFallback(tctx, cfg.Model, sysPrompt+"\n\n"+userPrompt); cliErr == nil && strings.TrimSpace(cliText) != "" {
			rawText = cliText
			err = nil
			if modelUsed == "" {
				modelUsed = "codex-cli"
			}
		} else if err == nil {
			err = cliErr
		}
	}

	latency := time.Since(start).Milliseconds()
	if err != nil || strings.TrimSpace(rawText) == "" {
		errMsg := "AI 暂不可用"
		if err != nil {
			errMsg = err.Error()
			if len(errMsg) > 120 {
				errMsg = errMsg[:120] + "..."
			}
		}
		return kbQAResult{Model: modelUsed, LatencyMs: latency, Error: errMsg}
	}

	ans, cites := splitAnswerAndCitations(rawText)
	return kbQAResult{
		Answer:    ans,
		Citations: cites,
		Model:     modelUsed,
		LatencyMs: latency,
	}
}

func callResponsesOrChatHTTP(ctx context.Context, cfg localLLMConfig, sysPrompt, userPrompt string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}

	// 1. Try OpenAI Responses API (/responses) first (works with Codex/aiseat gateways)
	respBody := map[string]any{
		"model":        cfg.Model,
		"instructions": sysPrompt,
		"input": []map[string]any{
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
	}
	if txt, err := postLLMJSON(ctx, client, cfg.BaseURL+"/responses", cfg.BearerToken, respBody); err == nil && txt != "" {
		return txt, nil
	}

	// 2. Try standard Chat Completions API (/chat/completions)
	chatBody := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": sysPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.2,
		"max_tokens":  300,
	}
	return postLLMJSON(ctx, client, cfg.BaseURL+"/chat/completions", cfg.BearerToken, chatBody)
}

func postLLMJSON(ctx context.Context, client *http.Client, endpoint, token string, body any) (string, error) {
	rawReq, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawReq))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return parseSSEText(resp.Body), nil
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	return extractLLMText(payload), nil
}

func parseSSEText(r io.Reader) string {
	var sb strings.Builder
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &ev) == nil && ev.Delta != "" {
			sb.WriteString(ev.Delta)
		}
	}
	return strings.TrimSpace(sb.String())
}

func extractLLMText(payload []byte) string {
	// Responses API shape
	var respAPI struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &respAPI); err != nil {
		return ""
	}
	if strings.TrimSpace(respAPI.OutputText) != "" {
		return strings.TrimSpace(respAPI.OutputText)
	}
	for _, out := range respAPI.Output {
		for _, c := range out.Content {
			if strings.TrimSpace(c.Text) != "" {
				return strings.TrimSpace(c.Text)
			}
		}
	}
	if len(respAPI.Choices) > 0 {
		return strings.TrimSpace(respAPI.Choices[0].Message.Content)
	}
	return ""
}

func callCodexExecFallback(ctx context.Context, model, prompt string) (string, error) {
	bin := envprobe.LookupBinary("codex")
	if bin == "" {
		return "", fmt.Errorf("codex CLI 未安装")
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("wb-qa-%d.txt", time.Now().UnixNano()))
	defer os.Remove(outFile)
	args := []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check", "-o", outFile}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = envprobe.ExpandedEnv()
	out, err := cmd.CombinedOutput()
	if raw, rerr := os.ReadFile(outFile); rerr == nil && strings.TrimSpace(string(raw)) != "" {
		return strings.TrimSpace(string(raw)), nil
	}
	if err != nil {
		return "", fmt.Errorf("codex exec failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

var citeTokenRe = regexp.MustCompile(`[A-Za-z0-9_.\-/]+\.[A-Za-z0-9]{1,8}:\d+(?:-\d+)?`)

func splitAnswerAndCitations(text string) (string, []string) {
	var answerLines []string
	var citations []string
	seen := make(map[string]bool)

	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "引用:") || strings.HasPrefix(t, "引用：") {
			for _, m := range citeTokenRe.FindAllString(t, -1) {
				if !seen[m] {
					seen[m] = true
					citations = append(citations, m)
				}
			}
			continue
		}
		answerLines = append(answerLines, t)
	}
	// Also extract inline file:line citations if LLM didn't emit a separate 引用: line
	if len(citations) == 0 {
		for _, m := range citeTokenRe.FindAllString(text, -1) {
			if !seen[m] && len(citations) < 4 {
				seen[m] = true
				citations = append(citations, m)
			}
		}
	}
	return strings.Join(answerLines, "\n"), citations
}
