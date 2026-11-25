package repo

import (
	"log"
	"strings"
	"time"

	"agentflow/internal/domain"
)

const (
	PstackRecallIntakeWorkflowID       = "pstack-recall-intake"
	PstackVerifiableDeliveryWorkflowID = "pstack-verifiable-delivery"
	PstackProveAuditWorkflowID         = "pstack-prove-audit"
	PstackSubtractRefactorWorkflowID   = "pstack-subtract-refactor"
)

func pstackBuiltinTemplates() []GenericTemplate {
	now := time.Now().UTC()
	coreRule := domain.RuleDefinition{
		ID:      "pstack-core-discipline",
		Name:    "pstack 工程纪律（五项核心原则）",
		Content: "1) recall：接续前先核对已有任务记录、知识库与当前代码，只读当前任务范围；2) subtract-before-you-add：优先复用和简化现有实现，不默认叠加复杂流程；3) sequence-verifiable-units：拆成独立可验证的小步单元逐一交付；4) prove-it-works：写明实际命令与验证结果，保留提交/文件/行号证据，明确标出未验证项；5) encode-lessons-in-structure：重复踩坑固化到代码、类型或检查中，经验沉淀为知识记录。",
	}

	makeNode := func(id, name, agentID string, x float64, artifact string) domain.WorkflowNode {
		return domain.WorkflowNode{
			ID:          id,
			Name:        name,
			Type:        "agent",
			AgentID:     agentID,
			MaxAttempts: 2,
			ArtifactOutputs: []domain.ArtifactContract{{
				Name:      id + "-report",
				Path:      artifact,
				MediaType: "text/markdown",
				Required:  true,
			}},
			Position: domain.Position{X: x, Y: 200},
		}
	}

	makeAgent := func(id, name, desc, prompt string) domain.AgentDefinition {
		return domain.AgentDefinition{
			ID:            id,
			Name:          name,
			Description:   desc,
			Version:       1,
			ExecutorID:    "codex",
			KnowledgeBase: true,
			PlatformMCP:   true,
			Rules:         []string{coreRule.ID},
			Prompt:        prompt + "\n\n要求：回复末尾附「知识记录」（标题、适用场景、原因、做法、验证依据）。\n\n任务输入：{{input}}",
		}
	}

	// 1. pstack · 现状核对与减法方案 SOP (recall + subtract-before-you-add + sequence-verifiable-units)
	t1 := GenericTemplate{
		Workflow: domain.WorkflowDefinition{
			ID:          PstackRecallIntakeWorkflowID,
			Name:        "pstack · 现状核对与减法方案 SOP",
			Description: "基于 pstack「recall + subtract-before-you-add + sequence-verifiable-units」拆分：先核对现状与代码边界，再做减法复用方案，最后拆成可验证小步单元。默认执行器：Codex。",
			Version:     1,
			Revision:    1,
			Status:      "published",
			Owner:       "平台",
			Generic:     true,
			UpdatedAt:   now,
			Nodes: []domain.WorkflowNode{
				makeNode("recall", "现状核对 (Recall)", "pstack-recall", 100, "recall-context.md"),
				makeNode("subtract", "减法方案 (Subtract First)", "pstack-subtract", 340, "subtract-plan.md"),
				makeNode("units", "小步拆解 (Verifiable Units)", "pstack-units", 580, "verifiable-units.md"),
			},
			Edges: []domain.WorkflowEdge{
				{ID: "e1", Source: "recall", Target: "subtract", When: "success"},
				{ID: "e2", Source: "subtract", Target: "units", When: "success"},
			},
		},
		Agents: []domain.AgentDefinition{
			makeAgent("pstack-recall", "pstack 现状核对师", "核对已有任务记录、知识库与源码现状，限定最小改动范围",
				"执行 pstack「recall」阶段：1) 优先查项目知识库和当前任务范围内的源码文件，不盲目通读无关目录；2) 梳理已有实现、依赖边界和历史约束；3) 输出带 `文件:行号` 依据的现状核对报告。"),
			makeAgent("pstack-subtract", "pstack 减法架构师", "坚持先减后加，优先复用与简化现有机制",
				"执行 pstack「subtract-before-you-add」阶段：基于现状核对报告，优先找能直接复用或删繁就简的方案；严禁引入多余抽象层或重复配置，给出最小化改动方案与风险说明。"),
			makeAgent("pstack-units", "pstack 小步拆解师", "把方案拆成独立可验证的最小交付单元",
				"执行 pstack「sequence-verifiable-units」阶段：将减法方案拆成 2–5 个按依赖顺序排列的最小可验证单元，每个单元写清：改动文件、预期行为、具体验证命令与回滚方式。"),
		},
		Rules: []domain.RuleDefinition{coreRule},
	}

	// 2. pstack · 小步迭代与实证交付 SOP (sequence-verifiable-units + prove-it-works + encode-lessons-in-structure)
	t2 := GenericTemplate{
		Workflow: domain.WorkflowDefinition{
			ID:          PstackVerifiableDeliveryWorkflowID,
			Name:        "pstack · 小步迭代与实证交付 SOP",
			Description: "基于 pstack「sequence-verifiable-units + prove-it-works + encode-lessons-in-structure」拆分：小步编码实现 → 真实运行实证验收 → 把经验固化进代码与知识库。默认执行器：Codex。",
			Version:     1,
			Revision:    1,
			Status:      "published",
			Owner:       "平台",
			Generic:     true,
			UpdatedAt:   now,
			Nodes: []domain.WorkflowNode{
				makeNode("implement", "小步实现 (Unit Build)", "pstack-implement", 100, "unit-implementation.md"),
				makeNode("prove", "实证验收 (Prove It Works)", "pstack-prove", 340, "proof-report.md"),
				makeNode("encode", "经验固化 (Encode Lessons)", "pstack-encode", 580, "lessons-encoded.md"),
			},
			Edges: []domain.WorkflowEdge{
				{ID: "e1", Source: "implement", Target: "prove", When: "success"},
				{ID: "e2", Source: "prove", Target: "encode", When: "success"},
			},
		},
		Agents: []domain.AgentDefinition{
			makeAgent("pstack-implement", "pstack 小步开发工程师", "按最小可验证单元实现改动，保持提交原子可回滚",
				"执行 pstack「sequence-verifiable-units」实现阶段：严格按最小单元修改目标代码，不夹带无关重构，保证改动清晰、边界明确、可独立回滚。"),
			makeAgent("pstack-prove", "pstack 实证验收工程师", "用真实命令、测试和结果证明功能有效，如实标出未验证项",
				"执行 pstack「prove-it-works」阶段：运行受影响模块的真实单测、构建或接口检查，完整记录执行命令、实际输出和通过结论；未覆盖的场景必须明确列入「未验证项」，严禁口头宣称通过。"),
			makeAgent("pstack-encode", "pstack 经验固化工程师", "将踩坑固化到类型/检查/文档，沉淀可检索知识记录",
				"执行 pstack「encode-lessons-in-structure」阶段：检查本次改动是否暴露了可固化的边界校验或单测，补齐防护后输出结构化知识记录供团队沉淀。"),
		},
		Rules: []domain.RuleDefinition{coreRule},
	}

	// 3. pstack · 实证复核与代码审查 SOP (prove-it-works + encode-lessons-in-structure)
	t3 := GenericTemplate{
		Workflow: domain.WorkflowDefinition{
			ID:          PstackProveAuditWorkflowID,
			Name:        "pstack · 实证复核与代码审查 SOP",
			Description: "基于 pstack「prove-it-works + encode-lessons-in-structure」拆分：专注独立代码审查、逻辑漏洞排查、实证证据核验与团队避坑规则沉淀。默认执行器：Codex。",
			Version:     1,
			Revision:    1,
			Status:      "published",
			Owner:       "平台",
			Generic:     true,
			UpdatedAt:   now,
			Nodes: []domain.WorkflowNode{
				makeNode("audit", "实证代码复核 (Evidence Audit)", "pstack-audit", 120, "audit-findings.md"),
				makeNode("distill", "规则沉淀 (Lesson Capture)", "pstack-distill", 400, "lesson-summary.md"),
			},
			Edges: []domain.WorkflowEdge{
				{ID: "e1", Source: "audit", Target: "distill", When: "success"},
			},
		},
		Agents: []domain.AgentDefinition{
			makeAgent("pstack-audit", "pstack 实证审查专家", "按阻断项/建议项严格复核代码逻辑、安全边界与实证依据",
				"执行 pstack「prove-it-works」审查阶段：逐条检查目标代码或 diff 的权限边界、并发安全、错误处理和契约一致性；所有缺陷必须附真实 `文件:行号` 证据并区分「阻断项」与「建议项」。"),
			makeAgent("pstack-distill", "pstack 知识沉淀专家", "把高频缺陷提炼为可复用检查规则与待确认经验记录",
				"执行 pstack「encode-lessons-in-structure」沉淀阶段：将本次审查发现的共性问题整理为可落地的检查规则和简洁知识记录，避免同类 bug 再次发生。"),
		},
		Rules: []domain.RuleDefinition{coreRule},
	}

	// 4. pstack · 减法重构与架构瘦身 SOP (subtract-before-you-add + sequence-verifiable-units + prove-it-works)
	t4 := GenericTemplate{
		Workflow: domain.WorkflowDefinition{
			ID:          PstackSubtractRefactorWorkflowID,
			Name:        "pstack · 减法重构与架构瘦身 SOP",
			Description: "基于 pstack「subtract-before-you-add + sequence-verifiable-units + prove-it-works」拆分：盘点冗余模块与重复配置 → 兼容前提下分步瘦身 → 回归行为等价证明。默认执行器：Codex。",
			Version:     1,
			Revision:    1,
			Status:      "published",
			Owner:       "平台",
			Generic:     true,
			UpdatedAt:   now,
			Nodes: []domain.WorkflowNode{
				makeNode("scan", "冗余盘点 (Redundancy Scan)", "pstack-scan", 100, "redundancy-scan.md"),
				makeNode("prune", "分步瘦身 (Incremental Prune)", "pstack-prune", 340, "prune-changes.md"),
				makeNode("verify", "回归证明 (Regression Prove)", "pstack-verify", 580, "regression-proof.md"),
			},
			Edges: []domain.WorkflowEdge{
				{ID: "e1", Source: "scan", Target: "prune", When: "success"},
				{ID: "e2", Source: "prune", Target: "verify", When: "success"},
			},
		},
		Agents: []domain.AgentDefinition{
			makeAgent("pstack-scan", "pstack 冗余盘点师", "识别重复实现、死代码与冗余配置，列出可删减清单",
				"执行 pstack「subtract-before-you-add」盘点阶段：定位目标范围内的重复逻辑、未使用分支与重叠配置，按收益与风险排序输出精简清单（附 `文件:行号`）。"),
			makeAgent("pstack-prune", "pstack 瘦身重构工程师", "在保持对外接口兼容前提下分步删除与合并冗余",
				"执行 pstack「sequence-verifiable-units」瘦身阶段：按小步原子改动安全合并或删减冗余代码，确保现有 API、配置与行为完全向后兼容。"),
			makeAgent("pstack-verify", "pstack 回归验收工程师", "执行回归测试并出具行为等价证明",
				"执行 pstack「prove-it-works」回归阶段：运行受影响模块的构建与测试，对比精简前后的关键行为，记录真实命令输出与删减统计。"),
		},
		Rules: []domain.RuleDefinition{coreRule},
	}

	return []GenericTemplate{t1, t2, t3, t4}
}

func isCodeBuddyOnlyModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == "default-model" || strings.HasPrefix(m, "codebuddy:") || m == "deepseek-v4.1-flash"
}

// EnsurePstackSOPsAndCodexDefaults seeds the 4 modular pstack built-in SOPs and
// switches legacy default "codebuddy" executors on built-in generic templates and
// project agents over to "codex".
func (s *Store) EnsurePstackSOPsAndCodexDefaults() {
	if s == nil {
		return
	}
	// 1. Seed the 4 built-in pstack SOPs if not yet stored
	for _, tpl := range pstackBuiltinTemplates() {
		if _, exists := s.GenericTemplateByWorkflow(tpl.Workflow.ID); !exists {
			if err := s.PutGenericTemplate(tpl); err != nil {
				log.Printf("webuddy: seed pstack SOP %s failed: %v", tpl.Workflow.ID, err)
			}
		}
	}

	// 2. Migrate existing generic_templates agents from default codebuddy -> codex
	var rows []genericTemplateRow
	if err := s.DB.Find(&rows).Error; err == nil {
		for _, row := range rows {
			tpl, err := decodeGenericTemplate(row.Payload)
			if err != nil {
				continue
			}
			changed := false
			for i := range tpl.Agents {
				execID := strings.TrimSpace(tpl.Agents[i].ExecutorID)
				if execID == "" || execID == "codebuddy" {
					tpl.Agents[i].ExecutorID = "codex"
					if isCodeBuddyOnlyModel(tpl.Agents[i].Model) {
						tpl.Agents[i].Model = ""
					}
					changed = true
				}
			}
			for i := range tpl.Workflow.Nodes {
				execID := strings.TrimSpace(tpl.Workflow.Nodes[i].ExecutorID)
				if execID == "codebuddy" {
					tpl.Workflow.Nodes[i].ExecutorID = "codex"
					changed = true
				}
			}
			if changed {
				_ = s.PutGenericTemplate(tpl)
			}
		}
	}

	// 3. Migrate project-level agents whose executorId is "" or "codebuddy" -> "codex"
	for _, ag := range s.Agents() {
		execID := strings.TrimSpace(ag.ExecutorID)
		if execID == "" || execID == "codebuddy" {
			ag.ExecutorID = "codex"
			if isCodeBuddyOnlyModel(ag.Model) {
				ag.Model = ""
			}
			_ = s.PutAgent(ag)
		}
	}
}
