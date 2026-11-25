package httpapi

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"agentflow/internal/builtinagent"
)

type kbQAResult struct {
	Answer    string   `json:"aiAnswer,omitempty"`
	Citations []string `json:"aiCitations,omitempty"`
	Model     string   `json:"aiModel,omitempty"`
	Provider  string   `json:"aiProvider,omitempty"`
	LatencyMs int64    `json:"aiLatencyMs,omitempty"`
	Error     string   `json:"aiError,omitempty"`
}

func synthesizeKBAnswer(ctx context.Context, mode, question, evidence string) kbQAResult {
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
		sysPrompt = "你是代码变更影响分析助手。严格根据提供的图谱分析结果，用不超过3句大白话（总字数≤110字）总结改动这里会影响哪些核心业务、风险大不大、最需要重点测什么。禁止使用Markdown星号或反引号，只输出纯中文文本，必须以完整句号结尾，禁止编造未出现的内容。"
		userPrompt = fmt.Sprintf("准备修改目标：%s\n\n图谱影响分析结果：\n%s", question, evidence)
	} else {
		sysPrompt = "你是代码库业务问答助手。严格根据下面提供的「知识库检索证据」回答用户问题：\n" +
			"1. 用不超过 3 句大白话直接回答（总字数 ≤ 120 字），禁止使用Markdown星号或反引号，禁止客套、禁止抄写文档元信息，必须以完整句号结尾。\n" +
			"2. 如果证据里有对应的源码位置（格式为 路径:行号），最后另起一行写最多 3 个最关键的位置，格式：引用: path/A.java:42, path/B.py:18\n" +
			"3. 如果证据里确实不包含该问题的答案，只回复一句：「当前知识库暂未记录该逻辑，建议换个更具体的模块/接口词或在工作台让 AI 查源码。」绝不硬凑或编造。"
		userPrompt = fmt.Sprintf("用户问题：%s\n\n知识库检索证据：\n%s", question, evidence)
	}

	res := builtinagent.Default().Complete(ctx, builtinagent.CompletionRequest{
		SystemPrompt: sysPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    650,
		Temperature:  0.2,
	})
	if res.Error != "" || strings.TrimSpace(res.Text) == "" {
		return kbQAResult{
			Model:     res.Model,
			Provider:  res.Provider,
			LatencyMs: res.LatencyMs,
			Error:     res.Error,
		}
	}
	ans, cites := splitAnswerAndCitations(res.Text)
	return kbQAResult{
		Answer:    ans,
		Citations: cites,
		Model:     res.Model,
		Provider:  res.Provider,
		LatencyMs: res.LatencyMs,
	}
}

var citeTokenRe = regexp.MustCompile(`[^\s,，、;；:："'"'()（）\[\]<>]+\.[A-Za-z0-9]{1,8}:\d+(?:-\d+)?`)

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
