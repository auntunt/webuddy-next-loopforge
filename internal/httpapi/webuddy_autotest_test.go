package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"agentflow/internal/domain"
)

func TestWebuddyAutoTestSolidifyReplaySelfHealFirewallAndRollback(t *testing.T) {
	autotestAllowLoopbackForTests.Store(true)
	defer autotestAllowLoopbackForTests.Store(false)

	tmpDir := t.TempDir()
	t.Setenv("AGENTFLOW_AUTOTEST_DIR", tmpDir)
	defaultAutoTestStore = &autoTestStore{}

	// Dynamic mock web app simulating:
	// v1: button#btn-calc-v1
	// v2 (UI refactor): button#btn-calc-v2-refactored with same business text & section context
	// v3 (session expired -> redirects /admin to /login with a fake button, testing Semantic Firewall!)
	var uiMode atomic.Int32
	uiMode.Store(1)

	targetApp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/dev":
			http.SetCookie(w, &http.Cookie{Name: "demo_sess", Value: "ceo-ok", Path: "/"})
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		case "/admin":
			if uiMode.Load() == 3 {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if uiMode.Load() == 1 {
				_, _ = w.Write([]byte(`<!doctype html><html><head><title>算薪管理后台</title></head><body>
					<main><section data-section="2026-11 算薪与对账中心">
						<h1>2026-11 算薪与 AI 对账总览</h1>
						<button id="btn-calc-v1" onclick="document.getElementById('out').innerText='对账完成: 0 差异'">执行算薪核验</button>
						<div id="out">待核验</div>
					</section></main></body></html>`))
				return
			}
			// Mode 2: Weekly UI refactor renamed button ID and tag structure, kept semantic text & context!
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>算薪管理后台</title></head><body>
				<main><section data-section="2026-11 算薪与对账中心">
					<h1>2026-11 算薪与 AI 对账总览</h1>
					<div class="toolbar"><button id="btn-calc-v2-refactored" class="primary-action" onclick="document.getElementById('out').innerText='对账完成: 0 差异'">执行算薪核验</button></div>
					<div id="out">待核验</div>
				</section></main></body></html>`))
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><body><section data-section="账号登录">
				<h1>请重新登录</h1>
				<button id="login-submit">执行算薪核验</button>
			</section></body></html>`))
		case "/api/payroll/runs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"period":"2026-11","status":"settled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer targetApp.Close()

	f := cvBNewHTTPFixture(t)
	_ = f.s.PutProject(domain.Project{ID: "proj-earn", Name: "we-earn", OwnerID: f.user.ID})
	router := f.h

	doJSON := func(method, path string, body any) (int, map[string]any) {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	// 1. Solidify Revision 1 with Precondition Auth Injection
	code, genRes := doJSON(http.MethodPost, "/api/webuddy/autotest/cases/generate", map[string]any{
		"projectId":          "proj-earn",
		"title":              "管理后台算薪核验闭环",
		"baseUrl":            targetApp.URL,
		"preconditionUrl":    "/auth/dev",
		"naturalRequirement": "进入管理后台点击执行算薪核验并校验接口",
		"steps": []map[string]any{
			{"name": "点击执行算薪核验按钮", "action": "click", "primarySelector": "#btn-calc-v1"},
			{"name": "校验算薪批次 API", "action": "assert_api", "targetUrl": "/api/payroll/runs", "expected": "2026-11"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("generate failed (%d): %v", code, genRes)
	}
	caseObj, _ := genRes["case"].(map[string]any)
	caseID, _ := caseObj["id"].(string)
	if caseID == "" {
		t.Fatalf("expected non-empty caseID")
	}

	// 2. Zero-Token Fast Deterministic Replay (Mode 1 -> passed, 0 tokens!)
	code, replayRes := doJSON(http.MethodPost, "/api/webuddy/autotest/cases/"+caseID+"/run", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("zero-token replay failed (%d): %v", code, replayRes)
	}
	runObj, _ := replayRes["run"].(map[string]any)
	if runObj["status"] != "passed" {
		t.Fatalf("expected passed status, got %v", runObj["status"])
	}
	if int(runObj["tokensUsed"].(float64)) != 0 {
		t.Fatalf("expected 0 tokens on replay, got %v", runObj["tokensUsed"])
	}

	// 3. Simulate Weekly UI Refactor (Mode 2: #btn-calc-v1 -> #btn-calc-v2-refactored)
	// Expect 5-Step Diagnosis + 3D Portrait Confidence >= 0.80 -> Healed + Revision 2!
	uiMode.Store(2)
	code, healRes := doJSON(http.MethodPost, "/api/webuddy/autotest/cases/"+caseID+"/run", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("self-heal run failed (%d): %v", code, healRes)
	}
	healRun, _ := healRes["run"].(map[string]any)
	if healRun["status"] != "healed" {
		t.Fatalf("expected healed status on UI refactor, got %v (run=%+v)", healRun["status"], healRun)
	}
	healDiff, _ := healRun["healDiff"].(map[string]any)
	if healDiff == nil || healDiff["newSelector"] != "#btn-calc-v2-refactored" {
		t.Fatalf("expected healed selector #btn-calc-v2-refactored, got %+v", healDiff)
	}
	if healDiff["confidence"].(float64) < 0.80 {
		t.Fatalf("expected confidence >= 0.80, got %v", healDiff["confidence"])
	}

	// 4. Test Rollback Guardrail + Precedent Recording
	code, rbRes := doJSON(http.MethodPost, "/api/webuddy/autotest/cases/"+caseID+"/rollback", map[string]any{
		"targetRevision": 1,
	})
	if code != http.StatusOK || rbRes["restoredFromRevision"].(float64) != 1 {
		t.Fatalf("rollback failed (%d): %v", code, rbRes)
	}

	// 5. Test Cross-Page Semantic Firewall (Mode 3: /admin redirects to /login which EVEN has a button with identical text!)
	// Semantic Firewall MUST block healing onto /login!
	uiMode.Store(3)
	code, fwRes := doJSON(http.MethodPost, "/api/webuddy/autotest/cases/"+caseID+"/run", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("firewall run HTTP failed (%d): %v", code, fwRes)
	}
	fwRun, _ := fwRes["run"].(map[string]any)
	if fwRun["status"] != "failed" {
		t.Fatalf("expected Semantic Firewall to block cross-page false positive on /login, got status=%v", fwRun["status"])
	}
	fwRunID, _ := fwRun["id"].(string)

	// 6. Test 1-Click Escalate Failed Run to Webuddy Issue ("异常精准找人拍板")
	code, issRes := doJSON(http.MethodPost, "/api/webuddy/autotest/runs/"+fwRunID+"/create-issue", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("escalate to issue failed (%d): %v", code, issRes)
	}
	issObj, _ := issRes["issue"].(map[string]any)
	if issObj["id"] == "" {
		t.Fatalf("expected created issue ID, got %+v", issRes)
	}
}
