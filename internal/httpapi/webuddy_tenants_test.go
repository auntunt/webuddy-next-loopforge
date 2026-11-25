package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"agentflow/internal/auth"
	"agentflow/internal/domain"
	"agentflow/internal/engine"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"
)

func TestWebuddyTenantIsolationAndPresetSOPs(t *testing.T) {
	t.Setenv("AGENTFLOW_AUTH_REQUIRED", "1")
	t.Setenv("AGENTFLOW_SESSION_SECRET", "tenant-isolation-secret")
	origStore := defaultTenantStore
	defaultTenantStore = &tenantStore{path: filepath.Join(t.TempDir(), "tenants.json")}
	t.Cleanup(func() { defaultTenantStore = origStore })

	s, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer s.Close()
	s.EnsurePstackSOPsAndCodexDefaults()
	authClient := auth.NewClientFromEnv()
	srv := New(s, engine.New(s, runtime.NewManager(filepath.Join(t.TempDir(), "workspaces"))), tracker.NewService(), "", "")

	mkUser := func(staffID int, login, name string) (domain.User, *http.Cookie) {
		u := domain.User{ID: int64(staffID), StaffID: staffID, LoginName: login, ChineseName: name}
		if err := s.PutUser(u); err != nil {
			t.Fatalf("PutUser %s: %v", login, err)
		}
		tok, err := authClient.SignUser(&auth.User{
			ID:          u.ID,
			StaffID:     staffID,
			LoginName:   login,
			ChineseName: name,
			AuthTime:    time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("SignUser %s: %v", login, err)
		}
		return u, &http.Cookie{Name: authCookieName, Value: tok}
	}
	alice, aliceCookie := mkUser(101, "alice", "Alice")
	_, bobCookie := mkUser(102, "bob", "Bob")
	_, carolCookie := mkUser(103, "carol", "Carol")

	doReq := func(method, path string, body any, ck *http.Cookie) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Content-Type", "application/json")
		if ck != nil {
			req.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	// 1. Alice creates a project in default tenant
	if err := s.PutProject(domain.Project{ID: "proj-alpha", Name: "Alpha", OwnerID: alice.ID}); err != nil {
		t.Fatalf("put proj-alpha: %v", err)
	}
	_ = defaultTenantStore.bindProjectTenant("proj-alpha", DefaultTenantID)

	// 2. Bob creates an isolated tenant "team-beta" via API (auto-switches Bob to team-beta)
	rec := doReq(http.MethodPost, "/api/tenants", map[string]any{
		"name":        "Team Beta",
		"description": "Isolated finance team",
	}, bobCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("create tenant failed: %d %s", rec.Code, rec.Body.String())
	}
	var createRes struct {
		Tenant Tenant `json:"tenant"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createRes)
	betaTID := createRes.Tenant.ID
	betaInvite := createRes.Tenant.InviteCode
	if betaTID == "" || betaInvite == "" {
		t.Fatalf("expected tenant id and invite code, got %+v", createRes.Tenant)
	}

	// 3. Bob creates a project inside team-beta
	rec = doReq(http.MethodPost, "/api/projects", map[string]any{
		"id":   "proj-beta",
		"name": "Beta Project",
	}, bobCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob create proj-beta failed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Bob adds Carol as a read-only viewer in team-beta
	rec = doReq(http.MethodPost, "/api/tenants/"+betaTID+"/members", map[string]any{
		"loginName": "carol",
		"role":      "viewer",
	}, bobCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("add carol viewer failed: %d %s", rec.Code, rec.Body.String())
	}
	// Switch Carol to team-beta
	rec = doReq(http.MethodPost, "/api/tenants/switch", map[string]any{"tenantId": betaTID}, carolCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch carol failed: %d %s", rec.Code, rec.Body.String())
	}

	// 5. Isolation check: Alice MUST NOT see proj-beta in /api/projects or /api/projects/proj-beta or preview
	rec = doReq(http.MethodGet, "/api/projects", nil, aliceCookie)
	if bytes.Contains(rec.Body.Bytes(), []byte("proj-beta")) {
		t.Fatalf("CROSS-TENANT LEAK: alice saw proj-beta in /api/projects: %s", rec.Body.String())
	}
	rec = doReq(http.MethodGet, "/api/projects/proj-beta", nil, aliceCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for alice reading proj-beta, got %d", rec.Code)
	}
	rec = doReq(http.MethodGet, "/api/webuddy/preview/status?projectId=proj-beta", nil, aliceCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for alice preview status on proj-beta, got %d", rec.Code)
	}
	rec = doReq(http.MethodGet, "/app-preview/proj-beta/", nil, aliceCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for alice app-preview on proj-beta, got %d", rec.Code)
	}

	// 6. Viewer check: Carol CAN read proj-beta, CANNOT write issue or quick-create SOP
	rec = doReq(http.MethodGet, "/api/projects/proj-beta", nil, carolCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for carol viewer reading proj-beta, got %d", rec.Code)
	}
	rec = doReq(http.MethodPost, "/api/webuddy/sop/quick-create", map[string]any{
		"projectId": "proj-beta",
		"name":      "Unauthorized SOP",
		"steps":     []map[string]string{{"name": "S1", "prompt": "P1"}},
	}, carolCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for carol viewer quick-create SOP, got %d", rec.Code)
	}

	// 7. Preset SOP protection: Bob CANNOT mutate canonical pstack preset in-place, and CANNOT publish generic SOP
	rec = doReq(http.MethodPut, "/api/workflows", map[string]any{
		"id":   repo.PstackVerifiableDeliveryWorkflowID,
		"name": "Hacked Preset",
	}, bobCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for bob editing platform preset SOP, got %d", rec.Code)
	}
	rec = doReq(http.MethodPost, "/api/webuddy/sop/quick-create", map[string]any{
		"projectId": "proj-beta",
		"name":      "Fake Generic",
		"generic":   true,
		"steps":     []map[string]string{{"name": "S1", "prompt": "P1"}},
	}, bobCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for bob creating generic SOP, got %d", rec.Code)
	}

	// 8. Bob CAN clone platform preset SOP into team-beta, and Alice MUST NOT see it
	rec = doReq(http.MethodPost, "/api/tenants/"+betaTID+"/sops/"+repo.PstackVerifiableDeliveryWorkflowID+"/clone", map[string]any{
		"name": "Beta 定制交付 SOP",
	}, bobCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob clone preset SOP failed: %d %s", rec.Code, rec.Body.String())
	}
	var cloneRes struct {
		Workflow domain.WorkflowDefinition `json:"workflow"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cloneRes)
	if cloneRes.Workflow.Generic || cloneRes.Workflow.Shared || cloneRes.Workflow.ID == repo.PstackVerifiableDeliveryWorkflowID {
		t.Fatalf("cloned SOP must be non-generic fresh ID, got %+v", cloneRes.Workflow)
	}
	rec = doReq(http.MethodGet, "/api/workflows", nil, aliceCookie)
	if bytes.Contains(rec.Body.Bytes(), []byte(cloneRes.Workflow.ID)) {
		t.Fatalf("CROSS-TENANT LEAK: alice saw team-beta cloned SOP %s", cloneRes.Workflow.ID)
	}

	// 9. Preset SOP policy filtering: Bob enables ONLY pstack-prove-audit in team-beta
	rec = doReq(http.MethodPut, "/api/tenants/"+betaTID, map[string]any{
		"presetSopPolicy":     "selected",
		"enabledPresetSopIds": []string{repo.PstackProveAuditWorkflowID},
	}, bobCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("update preset SOP policy failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(http.MethodGet, "/api/workflows", nil, bobCookie)
	bodyStr := rec.Body.String()
	if !bytes.Contains([]byte(bodyStr), []byte(repo.PstackProveAuditWorkflowID)) {
		t.Fatalf("expected pstack-prove-audit visible to bob")
	}
	if bytes.Contains([]byte(bodyStr), []byte(repo.PstackRecallIntakeWorkflowID)) {
		t.Fatalf("expected pstack-recall-intake hidden by team-beta selected policy")
	}

	// 10. Invite code join: Alice joins team-beta via invite code and gains read access to proj-beta
	rec = doReq(http.MethodPost, "/api/tenants/join", map[string]any{"inviteCode": betaInvite}, aliceCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice join via invite failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(http.MethodGet, "/api/projects/proj-beta", nil, aliceCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for alice after joining team-beta, got %d", rec.Code)
	}
}
