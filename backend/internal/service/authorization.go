package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func ContextPrincipal(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
func requireCapability(ctx context.Context, code string) error {
	p := ContextPrincipal(ctx)
	if p == nil || !p.HasPermission(code) {
		return ErrPermission
	}
	return nil
}

func workflowScopeQuery(query *gorm.DB, p *Principal, permission, column string) *gorm.DB {
	if p == nil || p.User == nil || !p.HasPermission(permission) {
		return query.Where("FALSE")
	}
	if p.HasRole("R_SUPER") {
		return query
	}
	return query.Where(fmt.Sprintf(`%s IN (SELECT w.id FROM workflows w WHERE w.owner_user_id=? OR EXISTS(SELECT 1 FROM workflow_user_grants g WHERE g.workflow_id=w.id AND g.user_id=? AND jsonb_exists(g.permissions,?)) OR EXISTS(SELECT 1 FROM workflow_role_grants g WHERE g.workflow_id=w.id AND g.role_id IN ? AND jsonb_exists(g.permissions,?)))`, column), p.User.ID, p.User.ID, permission, p.RoleIDs, permission)
}
func (a *App) AuthorizeWorkflow(ctx context.Context, id int64, permission string) error {
	if grant, ok := ctx.Value(resultGrantKey{}).(resultExecutionGrant); ok && grant.workflowID == id && grant.permissions[permission] {
		p, err := principalForTx(a.DB.WithContext(ctx), ContextPrincipal(ctx))
		if err != nil {
			return err
		}
		return a.validateResultExecutionGrant(a.DB.WithContext(ctx), ctx, p, grant)
	}
	if err := requireCapability(ctx, permission); err != nil {
		return err
	}
	var count int64
	q := workflowScopeQuery(a.DB.WithContext(ctx).Model(&db.Workflow{}), ContextPrincipal(ctx), permission, "workflows.id")
	if err := q.Where("workflows.id=?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: workflow", ErrNotFound)
	}
	return nil
}
func (a *App) authorizeRun(ctx context.Context, id int64, permission string) error {
	var run db.WorkflowRun
	if err := a.DB.WithContext(ctx).Select("workflow_id").First(&run, id).Error; err != nil {
		return ErrNotFound
	}
	return a.AuthorizeWorkflow(ctx, run.WorkflowID, permission)
}
func (a *App) authorizeExecution(userID int64, g validatedWorkflowGraph) error {
	p, err := a.buildPrincipal(userID)
	if err != nil {
		return ErrPermission
	}
	if !p.HasPermission("workflows.run") {
		return ErrPermission
	}
	for _, desc := range g.descriptors {
		for _, code := range desc.ExecutionPermissions {
			if !p.HasPermission(code) {
				return ErrPermission
			}
		}
	}
	return nil
}
func (a *App) RevalidateSession(p *Principal, permission string) (*Principal, error) {
	if p == nil || p.User == nil || !p.AccessTokenExp.After(time.Now().UTC()) || a.isAccessTokenRevoked(p.AccessTokenID) {
		return nil, ErrPermission
	}
	current, err := a.buildPrincipal(p.User.ID)
	if err != nil {
		return nil, err
	}
	current.AccessTokenID, current.AccessTokenExp = p.AccessTokenID, p.AccessTokenExp
	if permission != "" && !current.HasPermission(permission) {
		return nil, ErrPermission
	}
	return current, nil
}

type WorkflowGrant struct {
	UserID      int64    `json:"userId,omitempty"`
	RoleID      int64    `json:"roleId,omitempty"`
	Permissions []string `json:"permissions"`
}

func (a *App) ReplaceWorkflowGrants(ctx context.Context, id int64, grants []WorkflowGrant) error {
	if err := a.AuthorizeWorkflow(ctx, id, "workflows.share"); err != nil {
		return err
	}
	if len(grants) > 256 {
		return fmt.Errorf("too many workflow grants")
	}
	p := ContextPrincipal(ctx)
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthorization(tx); err != nil {
			return err
		}
		current, err := principalForTx(tx, p)
		if err != nil {
			return err
		}
		p = current
		if err := authorizeWorkflowTx(tx, p, id, "workflows.share"); err != nil {
			return err
		}
		var w db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w, id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM workflow_user_grants WHERE workflow_id=?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM workflow_role_grants WHERE workflow_id=?", id).Error; err != nil {
			return err
		}
		for _, g := range grants {
			if (g.UserID > 0) == (g.RoleID > 0) {
				return fmt.Errorf("grant must name one subject")
			}
			for _, code := range g.Permissions {
				if !workflowGrantPermissions[code] || !p.HasPermission(code) {
					return ErrPermission
				}
				if err := authorizeWorkflowTx(tx, p, id, code); err != nil {
					return err
				}
			}
			permissions, _ := json.Marshal(g.Permissions)
			if g.UserID > 0 {
				if err := tx.Exec("INSERT INTO workflow_user_grants(workflow_id,user_id,permissions) VALUES (?,?,?)", id, g.UserID, string(permissions)).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Exec("INSERT INTO workflow_role_grants(workflow_id,role_id,permissions) VALUES (?,?,?)", id, g.RoleID, string(permissions)).Error; err != nil {
					return err
				}
			}
		}
		return auditAuthorization(tx, p, "workflow.grants", fmt.Sprint(id))
	})
}

var workflowGrantPermissions = map[string]bool{
	"workflows.read": true, "workflows.update": true, "workflows.publish": true, "workflows.run": true, "workflows.activate": true, "workflows.cancel": true, "workflows.retry": true, "workflows.delete": true, "workflows.share": true, "workflows.secrets.manage": true, "human_tasks.read": true, "human_tasks.decide": true,
}

func auditAuthorization(tx *gorm.DB, p *Principal, action, id string) error {
	actor := p.User.ID
	return tx.Create(&db.AuditRecord{RequestID: rand.Text(), ActorUserID: &actor, Action: action, ResourcePath: id, Outcome: "success", StatusCode: 200, CreatedAt: time.Now().UTC()}).Error
}

func principalForTx(tx *gorm.DB, p *Principal) (*Principal, error) {
	if p == nil || p.User == nil {
		return nil, ErrPermission
	}
	if p.AccessTokenID != "" {
		var revoked int64
		if !p.AccessTokenExp.After(time.Now().UTC()) {
			return nil, ErrPermission
		}
		if err := tx.Model(&db.RevokedSession{}).Where("token_id=?", p.AccessTokenID).Count(&revoked).Error; err != nil {
			return nil, err
		}
		if revoked != 0 {
			return nil, ErrPermission
		}
	}
	var user db.SystemUser
	if err := tx.Where("id=? AND is_active", p.User.ID).First(&user).Error; err != nil {
		return nil, ErrPermission
	}
	roles, err := userRolesTx(tx, user.ID)
	if err != nil {
		return nil, err
	}
	next := *p
	next.User = &user
	next.RoleCodes = nil
	next.RoleIDs = nil
	next.PermissionCodes = map[string]bool{}
	for _, r := range roles {
		if r.IsEnabled {
			next.RoleCodes = append(next.RoleCodes, r.Code)
			next.RoleIDs = append(next.RoleIDs, r.ID)
		}
	}
	var codes []string
	if len(next.RoleIDs) > 0 {
		if err := tx.Model(&db.RolePermission{}).Where("role_id IN ?", next.RoleIDs).Distinct().Pluck("permission_code", &codes).Error; err != nil {
			return nil, err
		}
	}
	for _, code := range codes {
		next.PermissionCodes[code] = true
	}
	return &next, nil
}

// ponytail: 命令与授权变更共用事务锁；高并发写入时再拆为主体和资源锁。
// 必须先调用，再取得工作流、运行、任务等行锁，避免撤销和提交相互越过。
func commandPrincipalTx(tx *gorm.DB, ctx context.Context, permission string) (*Principal, error) {
	if err := lockAuthorization(tx); err != nil {
		return nil, err
	}
	p, err := principalForTx(tx, ContextPrincipal(ctx))
	if err != nil {
		return nil, err
	}
	if permission != "" && !p.HasPermission(permission) {
		return nil, ErrPermission
	}
	return p, nil
}
func (a *App) authorizeWorkflowCommandTx(tx *gorm.DB, ctx context.Context, id int64, permission string) (*Principal, error) {
	p, err := commandPrincipalTx(tx, ctx, "")
	if err != nil {
		return nil, err
	}
	if grant, ok := ctx.Value(resultGrantKey{}).(resultExecutionGrant); ok && grant.workflowID == id && grant.permissions[permission] {
		return p, a.validateResultExecutionGrant(tx, ctx, p, grant)
	}
	return p, authorizeWorkflowTx(tx, p, id, permission)
}
func authorizeWorkflowTx(tx *gorm.DB, p *Principal, id int64, permission string) error {
	var count int64
	if err := workflowScopeQuery(tx.Model(&db.Workflow{}), p, permission, "workflows.id").Where("workflows.id=?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrPermission
	}
	return nil
}

func (a *App) workflowPermissions(ctx context.Context, w db.Workflow) ([]string, error) {
	p := ContextPrincipal(ctx)
	if p == nil {
		return nil, ErrPermission
	}
	allowed := map[string]bool{}
	if p.HasRole("R_SUPER") || p.User.ID == w.OwnerUserID {
		for code := range workflowGrantPermissions {
			allowed[code] = true
		}
	} else {
		var rows []string
		if err := a.DB.WithContext(ctx).Raw("SELECT permissions FROM workflow_user_grants WHERE workflow_id=? AND user_id=? UNION ALL SELECT permissions FROM workflow_role_grants WHERE workflow_id=? AND role_id IN ?", w.ID, p.User.ID, w.ID, p.RoleIDs).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, raw := range rows {
			var codes []string
			if json.Unmarshal([]byte(raw), &codes) != nil {
				return nil, ErrConflict
			}
			for _, code := range codes {
				allowed[code] = true
			}
		}
	}
	result := []string{}
	for code := range allowed {
		if p.HasPermission(code) {
			result = append(result, code)
		}
	}
	sort.Strings(result)
	return result, nil
}
