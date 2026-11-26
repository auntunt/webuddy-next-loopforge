package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

func TestParseKBGeneratedPage_Formats(t *testing.T) {
	raw := "TITLE: 报销提交模块\nSUMMARY: 负责员工报销单创建与发票校验\n---\n## 职责\n处理报销申请 `server/routes/me.js:31`。\n## 核心流程\n校验参数并落库。"
	got, err := parseKBGeneratedPage(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "报销提交模块" || got.Summary != "负责员工报销单创建与发票校验" {
		t.Fatalf("unexpected title/summary: %+v", got)
	}
	if got.Body == "" {
		t.Fatalf("expected non-empty body")
	}

	// Fenced output should also parse cleanly
	fenced := "```markdown\n" + raw + "\n```"
	got2, err := parseKBGeneratedPage(fenced)
	if err != nil || got2.Title != "报销提交模块" {
		t.Fatalf("fenced parse failed: err=%v got=%+v", err, got2)
	}
}

func TestSyncProjectKBFilesToShared_SkipsStubsAndSyncsAnalyzed(t *testing.T) {
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer store.Close()

	tmpRepo := t.TempDir()
	modDir := filepath.Join(tmpRepo, ".tmh", "knowledge", "modules")
	flowDir := filepath.Join(tmpRepo, ".tmh", "knowledge", "flows")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(flowDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Unanalyzed stub (>260 bytes with facts block) MUST be skipped!
	stub := "---\ntitle: 未分析模块\nstatus: 未分析\n---\n<!-- tmh:facts -->\n" +
		"files: a.js, b.js, c.js, d.js, e.js, f.js, g.js, h.js, i.js, j.js\n<!-- /tmh:facts -->\n" +
		"本页尚未分析，等待 AI 补全，这里故意凑够超过两百六十个字节来验证布尔优先级修复是否真正生效，防止未分析空白桩泄露到共享知识库。"
	if err := os.WriteFile(filepath.Join(modDir, "stub.md"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Analyzed module page MUST be synced
	analyzedMod := "---\ntitle: 报销审核模块\nsummary: 管理端报销单审核\nstatus: 已确认\ncommit: abc1234\n---\n## 职责\n负责管理员审批 `server/routes/admin.js:20`。"
	if err := os.WriteFile(filepath.Join(modDir, "admin.md"), []byte(analyzedMod), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Analyzed flow page in flows/ MUST ALSO be synced
	analyzedFlow := "---\ntitle: 提交报销单流程\nsummary: POST /api/me/claims 完整链路\nstatus: 待确认\ncommit: abc1234\n---\n## 入口\n`server/routes/me.js:31`"
	if err := os.WriteFile(filepath.Join(flowDir, "submit-claim.md"), []byte(analyzedFlow), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = store.PutProject(domain.Project{ID: "p-sync-test", Name: "测试报销项目"})
	_ = store.PutRepository(domain.Repository{ID: "r-sync-test", ProjectID: "p-sync-test", Name: "repo", LocalPath: tmpRepo})

	origShared := defaultSharedKBStore
	tmpSharedPath := filepath.Join(t.TempDir(), "shared-kb.json")
	defaultSharedKBStore = &sharedKBStore{path: tmpSharedPath}
	defer func() { defaultSharedKBStore = origShared }()

	d := &deps{repo: store}
	count := d.syncProjectKBFilesToShared("p-sync-test", "tester")
	if count != 2 {
		t.Fatalf("expected 2 synced pages (skipping stub, syncing modules+flows), got %d", count)
	}
	items := defaultSharedKBStore.list(DefaultTenantID, "", "kb_page")
	if len(items) != 2 {
		t.Fatalf("expected 2 shared items in store, got %d", len(items))
	}
}
