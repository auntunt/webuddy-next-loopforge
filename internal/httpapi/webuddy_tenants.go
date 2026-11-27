package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"

	"github.com/gin-gonic/gin"
)

const DefaultTenantID = "default"

// TenantMember represents a user's membership and role inside a tenant workspace.
type TenantMember struct {
	UserID   int64  `json:"userId"`
	Role     string `json:"role"` // owner | admin | member | viewer
	JoinedAt string `json:"joinedAt,omitempty"`
}

// Tenant represents an isolated team workspace with its own projects, custom SOPs,
// team members, invite code, and platform preset SOP policy.
type Tenant struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Slug                string         `json:"slug"`
	Description         string         `json:"description,omitempty"`
	OwnerID             int64          `json:"ownerId,omitempty"`
	Members             []TenantMember `json:"members"`
	InviteCode          string         `json:"inviteCode,omitempty"`
	PresetSOPPolicy     string         `json:"presetSopPolicy"` // inherit_all | selected
	EnabledPresetSOPIDs []string       `json:"enabledPresetSopIds,omitempty"`
	CreatedAt           string         `json:"createdAt,omitempty"`
	UpdatedAt           string         `json:"updatedAt,omitempty"`
}

type tenantStateSnapshot struct {
	Tenants          map[string]Tenant `json:"tenants"`
	ProjectTenants   map[string]string `json:"projectTenants"`
	WorkflowTenants  map[string]string `json:"workflowTenants"`
	UserActiveTenant map[int64]string  `json:"userActiveTenant"`
}

type tenantStore struct {
	mu      sync.RWMutex
	loaded  bool
	path    string
	state   tenantStateSnapshot
}

var defaultTenantStore = &tenantStore{}

func randomInviteCode() string {
	var b [5]byte
	if _, err := rand.Read(b[:]); err == nil {
		return strings.ToUpper(hex.EncodeToString(b[:]))
	}
	return strings.ToUpper(strconv.FormatInt(time.Now().UnixNano()%100000000, 36))
}

func (s *tenantStore) ensureLoaded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	if s.path == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = os.TempDir()
		}
		s.path = filepath.Join(home, ".webuddy-tenants.json")
	}
	s.state = tenantStateSnapshot{
		Tenants:          make(map[string]Tenant),
		ProjectTenants:   make(map[string]string),
		WorkflowTenants:  make(map[string]string),
		UserActiveTenant: make(map[int64]string),
	}
	if raw, err := os.ReadFile(s.path); err == nil {
		var loaded tenantStateSnapshot
		if json.Unmarshal(raw, &loaded) == nil {
			if loaded.Tenants != nil {
				s.state.Tenants = loaded.Tenants
			}
			if loaded.ProjectTenants != nil {
				s.state.ProjectTenants = loaded.ProjectTenants
			}
			if loaded.WorkflowTenants != nil {
				s.state.WorkflowTenants = loaded.WorkflowTenants
			}
			if loaded.UserActiveTenant != nil {
				s.state.UserActiveTenant = loaded.UserActiveTenant
			}
		}
	}
	if _, ok := s.state.Tenants[DefaultTenantID]; !ok {
		now := time.Now().UTC().Format(time.RFC3339)
		s.state.Tenants[DefaultTenantID] = Tenant{
			ID:              DefaultTenantID,
			Name:            "默认团队空间",
			Slug:            "default",
			Description:     "平台默认团队工作空间，兼容历史项目与内置预设 SOP",
			InviteCode:      randomInviteCode(),
			PresetSOPPolicy: "inherit_all",
			CreatedAt:       now,
			UpdatedAt:       now,
		}
	}
	s.loaded = true
}

func (s *tenantStore) saveLocked() error {
	return writeAtomicJSON(s.path, s.state)
}

func normalizeTenantRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner":
		return "owner"
	case "admin":
		return "admin"
	case "viewer":
		return "viewer"
	default:
		return "member"
	}
}

func tenantMemberRole(t Tenant, userID int64) (string, bool) {
	if userID <= 0 {
		return "", false
	}
	if t.OwnerID > 0 && t.OwnerID == userID {
		return "owner", true
	}
	for _, m := range t.Members {
		if m.UserID == userID {
			return normalizeTenantRole(m.Role), true
		}
	}
	return "", false
}

// ensureUserInDefaultTenant lazily adds legacy users who belong to no tenant yet
// into the default tenant so existing deployments stay seamless.
func (s *tenantStore) ensureUserInDefaultTenant(userID int64, isPlatformAdmin bool) {
	if userID <= 0 {
		return
	}
	s.ensureLoaded()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.state.Tenants {
		if _, ok := tenantMemberRole(t, userID); ok {
			return
		}
	}
	def := s.state.Tenants[DefaultTenantID]
	role := "member"
	if isPlatformAdmin {
		role = "owner"
		if def.OwnerID == 0 {
			def.OwnerID = userID
		}
	}
	def.Members = append(def.Members, TenantMember{
		UserID:   userID,
		Role:     role,
		JoinedAt: time.Now().UTC().Format(time.RFC3339),
	})
	def.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.state.Tenants[DefaultTenantID] = def
	if _, hasActive := s.state.UserActiveTenant[userID]; !hasActive {
		s.state.UserActiveTenant[userID] = DefaultTenantID
	}
	_ = s.saveLocked()
}

func (s *tenantStore) projectTenant(projectID string) string {
	s.ensureLoaded()
	pid := strings.TrimSpace(projectID)
	if pid == "" {
		return DefaultTenantID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if tid := strings.TrimSpace(s.state.ProjectTenants[pid]); tid != "" {
		return tid
	}
	return DefaultTenantID
}

func (s *tenantStore) bindProjectTenant(projectID, tenantID string) error {
	s.ensureLoaded()
	pid := strings.TrimSpace(projectID)
	tid := strings.TrimSpace(tenantID)
	if pid == "" || tid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ProjectTenants[pid] = tid
	return s.saveLocked()
}

func (s *tenantStore) workflowTenant(wf domain.WorkflowDefinition) string {
	if wf.Generic || isCanonicalPresetWorkflowID(wf.ID) {
		return "" // platform preset SOP
	}
	s.ensureLoaded()
	s.mu.RLock()
	tid := strings.TrimSpace(s.state.WorkflowTenants[wf.ID])
	s.mu.RUnlock()
	if tid != "" {
		return tid
	}
	if strings.TrimSpace(wf.ProjectID) != "" {
		return s.projectTenant(wf.ProjectID)
	}
	return DefaultTenantID
}

func (s *tenantStore) bindWorkflowTenant(workflowID, tenantID string) error {
	s.ensureLoaded()
	wid := strings.TrimSpace(workflowID)
	tid := strings.TrimSpace(tenantID)
	if wid == "" || tid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.WorkflowTenants[wid] = tid
	return s.saveLocked()
}

func (s *tenantStore) getTenant(tenantID string) (Tenant, bool) {
	s.ensureLoaded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.state.Tenants[strings.TrimSpace(tenantID)]
	return t, ok
}

func (s *tenantStore) activeTenantForUser(userID int64, isPlatformAdmin bool) string {
	if userID <= 0 {
		return DefaultTenantID
	}
	s.ensureUserInDefaultTenant(userID, isPlatformAdmin)
	s.ensureLoaded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if active := strings.TrimSpace(s.state.UserActiveTenant[userID]); active != "" {
		if t, ok := s.state.Tenants[active]; ok {
			if isPlatformAdmin {
				return active
			}
			if _, mem := tenantMemberRole(t, userID); mem {
				return active
			}
		}
	}
	// Fallback to first tenant the user belongs to
	ids := make([]string, 0, len(s.state.Tenants))
	for id := range s.state.Tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, mem := tenantMemberRole(s.state.Tenants[id], userID); mem {
			return id
		}
	}
	if isPlatformAdmin {
		return DefaultTenantID
	}
	return ""
}

func (s *tenantStore) userTenantRole(tenantID string, userID int64) (string, bool) {
	s.ensureLoaded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.state.Tenants[strings.TrimSpace(tenantID)]
	if !ok {
		return "", false
	}
	return tenantMemberRole(t, userID)
}

func (s *tenantStore) presetSOPEnabledInTenant(tenantID, workflowID string) bool {
	t, ok := s.getTenant(tenantID)
	if !ok {
		return true
	}
	if t.PresetSOPPolicy != "selected" {
		return true
	}
	for _, id := range t.EnabledPresetSOPIDs {
		if id == workflowID {
			return true
		}
	}
	return false
}

func isCanonicalPresetWorkflowID(id string) bool {
	switch strings.TrimSpace(id) {
	case repo.PstackRecallIntakeWorkflowID,
		repo.PstackVerifiableDeliveryWorkflowID,
		repo.PstackProveAuditWorkflowID,
		repo.PstackSubtractRefactorWorkflowID:
		return true
	default:
		return false
	}
}

// ---------- deps tenant-aware scoping helpers ----------

func (d deps) currentActiveTenant(r *http.Request) string {
	if !authRequired() {
		return DefaultTenantID
	}
	user, ok := d.requestUser(r, true)
	if !ok || user == nil || user.ID <= 0 {
		return DefaultTenantID
	}
	return defaultTenantStore.activeTenantForUser(user.ID, d.isPlatformAdmin(r))
}

func (d deps) userCanAccessTenant(userID int64, isPlatformAdmin bool, tenantID string) bool {
	if !authRequired() || isPlatformAdmin {
		return true
	}
	if userID <= 0 {
		return true // service token without user identity
	}
	defaultTenantStore.ensureUserInDefaultTenant(userID, isPlatformAdmin)
	_, ok := defaultTenantStore.userTenantRole(tenantID, userID)
	return ok
}

func (d deps) userCanManageTenant(userID int64, isPlatformAdmin bool, tenantID string) bool {
	if !authRequired() || isPlatformAdmin {
		return true
	}
	if userID <= 0 {
		return false
	}
	role, ok := defaultTenantStore.userTenantRole(tenantID, userID)
	return ok && (role == "owner" || role == "admin")
}

func (d deps) visibleProjectIDSetForRequest(r *http.Request) map[string]bool {
	activeTID := d.currentActiveTenant(r)
	out := make(map[string]bool)
	for _, p := range d.repo.Projects() {
		if defaultTenantStore.projectTenant(p.ID) == activeTID {
			out[p.ID] = true
		}
	}
	return out
}

func (d deps) visibleProjectsForRequest(r *http.Request, all []domain.Project) []domain.Project {
	if !authRequired() {
		return all
	}
	activeTID := d.currentActiveTenant(r)
	if activeTID == "" {
		return []domain.Project{}
	}
	out := make([]domain.Project, 0, len(all))
	for _, p := range all {
		if defaultTenantStore.projectTenant(p.ID) == activeTID {
			out = append(out, p)
		}
	}
	return out
}

func (d deps) visibleWorkflowsForRequest(r *http.Request, all []domain.WorkflowDefinition) []domain.WorkflowDefinition {
	if !authRequired() {
		return all
	}
	activeTID := d.currentActiveTenant(r)
	if activeTID == "" {
		return []domain.WorkflowDefinition{}
	}
	out := make([]domain.WorkflowDefinition, 0, len(all))
	for _, wf := range all {
		if wf.Generic || isCanonicalPresetWorkflowID(wf.ID) {
			if defaultTenantStore.presetSOPEnabledInTenant(activeTID, wf.ID) {
				out = append(out, wf)
			}
			continue
		}
		if defaultTenantStore.workflowTenant(wf) == activeTID {
			out = append(out, wf)
		}
	}
	return out
}

func (d deps) visibleUsersForTenant(r *http.Request, all []domain.User) []domain.User {
	if !authRequired() {
		return all
	}
	activeTID := d.currentActiveTenant(r)
	t, ok := defaultTenantStore.getTenant(activeTID)
	if !ok {
		return []domain.User{}
	}
	memberSet := make(map[int64]bool, len(t.Members)+1)
	if t.OwnerID > 0 {
		memberSet[t.OwnerID] = true
	}
	for _, m := range t.Members {
		memberSet[m.UserID] = true
	}
	out := make([]domain.User, 0, len(all))
	for _, u := range all {
		if memberSet[u.ID] || (activeTID == DefaultTenantID && d.repo.IsPlatformAdmin(u.LoginName)) {
			out = append(out, u)
		}
	}
	return out
}

// ---------- HTTP Handlers for Tenant & Team Collaboration ----------

type tenantSummaryDTO struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	Slug                string             `json:"slug"`
	Description         string             `json:"description,omitempty"`
	OwnerID             int64              `json:"ownerId,omitempty"`
	MyRole              string             `json:"myRole"`
	MemberCount         int                `json:"memberCount"`
	ProjectCount        int                `json:"projectCount"`
	InviteCode          string             `json:"inviteCode,omitempty"`
	PresetSOPPolicy     string             `json:"presetSopPolicy"`
	EnabledPresetSOPIDs []string           `json:"enabledPresetSopIds,omitempty"`
	Members             []tenantMemberView `json:"members,omitempty"`
	CreatedAt           string             `json:"createdAt,omitempty"`
	UpdatedAt           string             `json:"updatedAt,omitempty"`
}

type tenantMemberView struct {
	UserID      int64  `json:"userId"`
	LoginName   string `json:"loginName"`
	ChineseName string `json:"chineseName"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joinedAt,omitempty"`
}

func (h *webuddyEnvHandlers) buildTenantSummaries(r *http.Request) ([]tenantSummaryDTO, string) {
	defaultTenantStore.ensureLoaded()
	user, hasUser := h.requestUser(r, true)
	isAdmin := h.isPlatformAdmin(r)
	var uid int64
	if hasUser && user != nil {
		uid = user.ID
		defaultTenantStore.ensureUserInDefaultTenant(uid, isAdmin)
	}
	activeTID := h.currentActiveTenant(r)

	userMap := make(map[int64]domain.User)
	for _, u := range h.repo.Users() {
		userMap[u.ID] = u
	}
	projCounts := make(map[string]int)
	for _, p := range h.repo.Projects() {
		projCounts[defaultTenantStore.projectTenant(p.ID)]++
	}

	defaultTenantStore.mu.RLock()
	tenants := make([]Tenant, 0, len(defaultTenantStore.state.Tenants))
	for _, t := range defaultTenantStore.state.Tenants {
		tenants = append(tenants, t)
	}
	defaultTenantStore.mu.RUnlock()

	sort.Slice(tenants, func(i, j int) bool {
		if tenants[i].ID == DefaultTenantID {
			return true
		}
		if tenants[j].ID == DefaultTenantID {
			return false
		}
		return tenants[i].ID < tenants[j].ID
	})

	out := make([]tenantSummaryDTO, 0, len(tenants))
	for _, t := range tenants {
		role, isMember := tenantMemberRole(t, uid)
		if !isMember && !isAdmin && authRequired() {
			continue
		}
		if !isMember && isAdmin {
			role = "platform_admin"
		}
		canManage := isAdmin || role == "owner" || role == "admin"
		dto := tenantSummaryDTO{
			ID:                  t.ID,
			Name:                t.Name,
			Slug:                t.Slug,
			Description:         t.Description,
			OwnerID:             t.OwnerID,
			MyRole:              role,
			MemberCount:         len(t.Members),
			ProjectCount:        projCounts[t.ID],
			PresetSOPPolicy:     t.PresetSOPPolicy,
			EnabledPresetSOPIDs: t.EnabledPresetSOPIDs,
			CreatedAt:           t.CreatedAt,
			UpdatedAt:           t.UpdatedAt,
		}
		if canManage {
			dto.InviteCode = t.InviteCode
		}
		members := make([]tenantMemberView, 0, len(t.Members))
		for _, m := range t.Members {
			u := userMap[m.UserID]
			members = append(members, tenantMemberView{
				UserID:      m.UserID,
				LoginName:   u.LoginName,
				ChineseName: u.ChineseName,
				Role:        normalizeTenantRole(m.Role),
				JoinedAt:    m.JoinedAt,
			})
		}
		dto.Members = members
		out = append(out, dto)
	}
	return out, activeTID
}

// GET /api/tenants
func (h *webuddyEnvHandlers) listTenants(c *gin.Context) {
	items, activeTID := h.buildTenantSummaries(c.Request)
	var presets []gin.H
	for _, wf := range h.repo.Workflows() {
		if wf.Generic || isCanonicalPresetWorkflowID(wf.ID) {
			presets = append(presets, gin.H{
				"id":          wf.ID,
				"name":        wf.Name,
				"description": wf.Description,
				"steps":       len(wf.Nodes),
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"activeTenantId": activeTID,
		"tenants":        items,
		"presetSops":     presets,
	})
}

// POST /api/tenants
func (h *webuddyEnvHandlers) createTenant(c *gin.Context) {
	user, ok := h.requestUser(c.Request, true)
	if authRequired() && (!ok || user == nil || user.ID <= 0) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	var req struct {
		Name            string `json:"name"`
		Description     string `json:"description"`
		PresetSOPPolicy string `json:"presetSopPolicy"`
	}
	if !decode(c, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "租户名称不能为空"})
		return
	}
	slug := sanitizeRepoSlug(name)
	if slug == "" {
		slug = "team"
	}
	suffix := randomID()
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}
	tid := "tenant-" + slug + "-" + suffix
	policy := "inherit_all"
	if req.PresetSOPPolicy == "selected" {
		policy = "selected"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var ownerID int64
	var members []TenantMember
	if user != nil && user.ID > 0 {
		ownerID = user.ID
		members = []TenantMember{{UserID: user.ID, Role: "owner", JoinedAt: now}}
	}
	t := Tenant{
		ID:              tid,
		Name:            name,
		Slug:            slug,
		Description:     strings.TrimSpace(req.Description),
		OwnerID:         ownerID,
		Members:         members,
		InviteCode:      randomInviteCode(),
		PresetSOPPolicy: policy,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	defaultTenantStore.ensureLoaded()
	defaultTenantStore.mu.Lock()
	defaultTenantStore.state.Tenants[tid] = t
	if ownerID > 0 {
		defaultTenantStore.state.UserActiveTenant[ownerID] = tid
	}
	err := defaultTenantStore.saveLocked()
	defaultTenantStore.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items, activeTID := h.buildTenantSummaries(c.Request)
	c.JSON(http.StatusOK, gin.H{"ok": true, "tenant": t, "activeTenantId": activeTID, "tenants": items})
}

// POST /api/tenants/switch
func (h *webuddyEnvHandlers) switchTenant(c *gin.Context) {
	user, ok := h.requestUser(c.Request, true)
	if authRequired() && (!ok || user == nil || user.ID <= 0) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	var req struct {
		TenantID string `json:"tenantId"`
	}
	if !decode(c, &req) {
		return
	}
	tid := strings.TrimSpace(req.TenantID)
	t, exists := defaultTenantStore.getTenant(tid)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "目标租户不存在"})
		return
	}
	var uid int64
	if user != nil {
		uid = user.ID
	}
	if !h.userCanAccessTenant(uid, h.isPlatformAdmin(c.Request), t.ID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "您不是该租户成员，无法切换"})
		return
	}
	if user != nil && user.ID > 0 {
		defaultTenantStore.mu.Lock()
		defaultTenantStore.state.UserActiveTenant[user.ID] = t.ID
		_ = defaultTenantStore.saveLocked()
		defaultTenantStore.mu.Unlock()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "activeTenantId": t.ID, "tenantName": t.Name})
}

// POST /api/tenants/join
func (h *webuddyEnvHandlers) joinTenantByInvite(c *gin.Context) {
	user, ok := h.requestUser(c.Request, true)
	if authRequired() && (!ok || user == nil || user.ID <= 0) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	var req struct {
		InviteCode string `json:"inviteCode"`
	}
	if !decode(c, &req) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.InviteCode))
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入邀请码"})
		return
	}
	defaultTenantStore.ensureLoaded()
	defaultTenantStore.mu.Lock()
	var matchedID string
	for id, t := range defaultTenantStore.state.Tenants {
		if strings.EqualFold(strings.TrimSpace(t.InviteCode), code) {
			matchedID = id
			break
		}
	}
	if matchedID == "" {
		defaultTenantStore.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "邀请码无效或已过期"})
		return
	}
	t := defaultTenantStore.state.Tenants[matchedID]
	if user != nil && user.ID > 0 {
		if _, already := tenantMemberRole(t, user.ID); !already {
			t.Members = append(t.Members, TenantMember{
				UserID:   user.ID,
				Role:     "member",
				JoinedAt: time.Now().UTC().Format(time.RFC3339),
			})
			t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			defaultTenantStore.state.Tenants[matchedID] = t
		}
		defaultTenantStore.state.UserActiveTenant[user.ID] = matchedID
		_ = defaultTenantStore.saveLocked()
	}
	defaultTenantStore.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ok": true, "activeTenantId": matchedID, "tenantName": t.Name})
}

// PUT /api/tenants/:id
func (h *webuddyEnvHandlers) updateTenant(c *gin.Context) {
	user, _ := h.requestUser(c.Request, true)
	tid := strings.TrimSpace(c.Param("id"))
	var uid int64
	if user != nil {
		uid = user.ID
	}
	if !h.userCanManageTenant(uid, h.isPlatformAdmin(c.Request), tid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有租户管理员或平台管理员可以修改租户设置"})
		return
	}
	var req struct {
		Name                *string  `json:"name"`
		Description         *string  `json:"description"`
		PresetSOPPolicy     *string  `json:"presetSopPolicy"`
		EnabledPresetSOPIDs []string `json:"enabledPresetSopIds"`
	}
	if !decode(c, &req) {
		return
	}
	defaultTenantStore.ensureLoaded()
	defaultTenantStore.mu.Lock()
	t, ok := defaultTenantStore.state.Tenants[tid]
	if !ok {
		defaultTenantStore.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "租户不存在"})
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		t.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		t.Description = strings.TrimSpace(*req.Description)
	}
	if req.PresetSOPPolicy != nil {
		if *req.PresetSOPPolicy == "selected" {
			t.PresetSOPPolicy = "selected"
		} else {
			t.PresetSOPPolicy = "inherit_all"
		}
	}
	if req.EnabledPresetSOPIDs != nil {
		t.EnabledPresetSOPIDs = req.EnabledPresetSOPIDs
	}
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	defaultTenantStore.state.Tenants[tid] = t
	err := defaultTenantStore.saveLocked()
	defaultTenantStore.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "tenant": t})
}

// POST /api/tenants/:id/invite/rotate
func (h *webuddyEnvHandlers) rotateTenantInvite(c *gin.Context) {
	user, _ := h.requestUser(c.Request, true)
	tid := strings.TrimSpace(c.Param("id"))
	var uid int64
	if user != nil {
		uid = user.ID
	}
	if !h.userCanManageTenant(uid, h.isPlatformAdmin(c.Request), tid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有租户管理员或平台管理员可以刷新邀请码"})
		return
	}
	defaultTenantStore.ensureLoaded()
	defaultTenantStore.mu.Lock()
	t, ok := defaultTenantStore.state.Tenants[tid]
	if !ok {
		defaultTenantStore.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "租户不存在"})
		return
	}
	t.InviteCode = randomInviteCode()
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	defaultTenantStore.state.Tenants[tid] = t
	err := defaultTenantStore.saveLocked()
	defaultTenantStore.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "inviteCode": t.InviteCode})
}

// POST /api/tenants/:id/members
func (h *webuddyEnvHandlers) upsertTenantMember(c *gin.Context) {
	user, _ := h.requestUser(c.Request, true)
	tid := strings.TrimSpace(c.Param("id"))
	var uid int64
	if user != nil {
		uid = user.ID
	}
	if !h.userCanManageTenant(uid, h.isPlatformAdmin(c.Request), tid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有租户管理员或平台管理员可以管理团队成员"})
		return
	}
	var req struct {
		UserID    int64  `json:"userId"`
		LoginName string `json:"loginName"`
		Role      string `json:"role"`
	}
	if !decode(c, &req) {
		return
	}
	targetUID := req.UserID
	if targetUID <= 0 && strings.TrimSpace(req.LoginName) != "" {
		login := strings.TrimSpace(req.LoginName)
		for _, u := range h.repo.Users() {
			if strings.EqualFold(u.LoginName, login) {
				targetUID = u.ID
				break
			}
		}
	}
	if targetUID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未找到目标用户，请确认用户名或 ID"})
		return
	}
	role := normalizeTenantRole(req.Role)
	defaultTenantStore.ensureLoaded()
	defaultTenantStore.mu.Lock()
	t, ok := defaultTenantStore.state.Tenants[tid]
	if !ok {
		defaultTenantStore.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "租户不存在"})
		return
	}
	updated := false
	for i := range t.Members {
		if t.Members[i].UserID == targetUID {
			t.Members[i].Role = role
			updated = true
			break
		}
	}
	if !updated {
		t.Members = append(t.Members, TenantMember{
			UserID:   targetUID,
			Role:     role,
			JoinedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	defaultTenantStore.state.Tenants[tid] = t
	err := defaultTenantStore.saveLocked()
	defaultTenantStore.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items, activeTID := h.buildTenantSummaries(c.Request)
	c.JSON(http.StatusOK, gin.H{"ok": true, "activeTenantId": activeTID, "tenants": items})
}

// DELETE /api/tenants/:id/members/:userId
func (h *webuddyEnvHandlers) removeTenantMember(c *gin.Context) {
	user, _ := h.requestUser(c.Request, true)
	tid := strings.TrimSpace(c.Param("id"))
	var uid int64
	if user != nil {
		uid = user.ID
	}
	if !h.userCanManageTenant(uid, h.isPlatformAdmin(c.Request), tid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有租户管理员或平台管理员可以移除团队成员"})
		return
	}
	targetUID, err := strconv.ParseInt(strings.TrimSpace(c.Param("userId")), 10, 64)
	if err != nil || targetUID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 ID"})
		return
	}
	defaultTenantStore.ensureLoaded()
	defaultTenantStore.mu.Lock()
	t, ok := defaultTenantStore.state.Tenants[tid]
	if !ok {
		defaultTenantStore.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "租户不存在"})
		return
	}
	if t.OwnerID == targetUID {
		defaultTenantStore.mu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能移除租户 Owner"})
		return
	}
	next := make([]TenantMember, 0, len(t.Members))
	for _, m := range t.Members {
		if m.UserID != targetUID {
			next = append(next, m)
		}
	}
	t.Members = next
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	defaultTenantStore.state.Tenants[tid] = t
	saveErr := defaultTenantStore.saveLocked()
	defaultTenantStore.mu.Unlock()
	if saveErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": saveErr.Error()})
		return
	}
	items, activeTID := h.buildTenantSummaries(c.Request)
	c.JSON(http.StatusOK, gin.H{"ok": true, "activeTenantId": activeTID, "tenants": items})
}

// POST /api/tenants/:id/sops/:sopId/clone
// Clones a read-only platform preset SOP into a tenant-owned customizable SOP
// (Generic=false, Shared=false, fresh ID, cloned Codex stage agents).
func (h *webuddyEnvHandlers) clonePresetSOPToTenant(c *gin.Context) {
	user, _ := h.requestUser(c.Request, true)
	tid := strings.TrimSpace(c.Param("id"))
	sopID := strings.TrimSpace(c.Param("sopId"))
	var uid int64
	if user != nil {
		uid = user.ID
	}
	if !h.userCanManageTenant(uid, h.isPlatformAdmin(c.Request), tid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有租户管理员或平台管理员可以定制预设 SOP"})
		return
	}
	t, exists := defaultTenantStore.getTenant(tid)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "租户不存在"})
		return
	}
	src, ok := h.repo.Workflow(sopID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "预设 SOP 不存在"})
		return
	}
	if !src.Generic && !isCanonicalPresetWorkflowID(src.ID) && defaultTenantStore.workflowTenant(src) != tid {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权复制其他租户的私有 SOP"})
		return
	}
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"projectId"`
	}
	_ = c.ShouldBindJSON(&req)
	targetProjectID := strings.TrimSpace(req.ProjectID)
	if targetProjectID != "" && defaultTenantStore.projectTenant(targetProjectID) != tid {
		c.JSON(http.StatusForbidden, gin.H{"error": "目标项目不属于当前租户"})
		return
	}

	suffix := randomID()
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}
	newWfID := fmt.Sprintf("%s-%s-%s", sanitizeRepoSlug(t.Slug), sanitizeRepoSlug(src.ID), suffix)
	newName := strings.TrimSpace(req.Name)
	if newName == "" {
		newName = fmt.Sprintf("%s（%s 定制版）", src.Name, t.Name)
	}
	actor := t.Name
	if user != nil && strings.TrimSpace(user.LoginName) != "" {
		actor = strings.TrimSpace(user.LoginName)
	}

	// Clone stage agents cleanly into fresh IDs so tenant edits never mutate platform presets
	clonedNodes := make([]domain.WorkflowNode, len(src.Nodes))
	for i, n := range src.Nodes {
		clonedNodes[i] = n
		if n.AgentID != "" {
			if srcAg, found := h.repo.Agent(n.AgentID); found {
				newAg := srcAg
				newAg.ID = fmt.Sprintf("%s-ag-%d", newWfID, i+1)
				newAg.ProjectID = targetProjectID
				if newAg.ExecutorID == "" {
					newAg.ExecutorID = "codex"
				}
				if err := h.repo.PutAgent(newAg); err == nil {
					clonedNodes[i].AgentID = newAg.ID
				}
			}
		}
	}

	now := time.Now().UTC()
	cloned := src
	cloned.ID = newWfID
	cloned.Name = newName
	cloned.ProjectID = targetProjectID
	cloned.Generic = false
	cloned.Shared = targetProjectID == ""
	cloned.Status = "published"
	cloned.Owner = actor
	cloned.Version = 1
	cloned.Revision = 1
	cloned.Nodes = clonedNodes
	cloned.CreatedAt = now
	cloned.UpdatedAt = now

	if err := h.repo.PutWorkflow(cloned); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存租户定制 SOP 失败: %v", err)})
		return
	}
	_ = defaultTenantStore.bindWorkflowTenant(cloned.ID, tid)
	c.JSON(http.StatusOK, gin.H{"ok": true, "workflow": cloned})
}
