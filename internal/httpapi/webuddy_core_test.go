package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"agentflow/internal/engine"
	"agentflow/internal/httpapi"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"
)

func TestWebuddyCodeReviewAndKBEndpoints(t *testing.T) {
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer store.Close()
	router := httpapi.New(store, engine.New(store, runtime.NewManager(filepath.Join(t.TempDir(), "workspaces"))), tracker.NewService(), "", "")

	// 1. Create a batched code review
	body := `{"projectId":"p1","title":"API 权限审查","writer":"codex","reviewer":"claude-code","diff":"diff --git a/api.go b/api.go\n--- a/api.go\n+++ b/api.go\n@@ -1 +1 @@\n+func handle() {}\n","blocking":["api.go:1 缺少鉴权"]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/codereview/reviews", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/codereview/reviews status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Binding string `json:"binding"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" || created.Verdict != "fail" {
		t.Fatalf("unexpected created review: %s", w.Body.String())
	}

	// 2. Submit human feedback
	fbBody, _ := json.Marshal(map[string]any{
		"requestId": "req-http-1",
		"binding":   created.Binding,
		"minutes":   10,
		"findings":  []map[string]any{{"id": "blocking:0", "verdict": "valid", "reason": "确认缺少鉴权"}},
		"missed":    []string{},
		"note":      "已核查 api.go",
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/codereview/reviews/"+created.ID+"/feedback", bytes.NewReader(fbBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST feedback status=%d body=%s", w.Code, w.Body.String())
	}

	// 3. Check summary
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/codereview/summary?projectId=p1", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"validRate":100`)) {
		t.Fatalf("GET summary status=%d body=%s", w.Code, w.Body.String())
	}

	// 4. Webuddy KB overview (builtin domain docs)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/webuddy/kb/overview", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`crypto-sm.md`)) {
		t.Fatalf("GET /api/webuddy/kb/overview status=%d body=%s", w.Code, w.Body.String())
	}

	// 5. Webuddy KB search
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/webuddy/kb/search", bytes.NewBufferString(`{"query":"SM4"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`builtin:crypto-sm.md`)) {
		t.Fatalf("POST /api/webuddy/kb/search status=%d body=%s", w.Code, w.Body.String())
	}

	// 6. Code review CLI executors endpoint
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/codereview/executors", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"codex"`)) {
		t.Fatalf("GET /api/codereview/executors status=%d body=%s", w.Code, w.Body.String())
	}

	// 7. Webuddy KB Streamable HTTP MCP: tools/list + tools/call(kb_search)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/webuddy/kb/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`kb_verify_evidence`)) {
		t.Fatalf("POST /api/webuddy/kb/mcp tools/list status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/webuddy/kb/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kb_search","arguments":{"query":"SM4"}}}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`builtin:crypto-sm.md`)) {
		t.Fatalf("POST /api/webuddy/kb/mcp tools/call status=%d body=%s", w.Code, w.Body.String())
	}
}
