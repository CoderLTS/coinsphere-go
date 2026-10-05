package service

import (
	"coinsphere/backend/internal/db"
	"fmt"
	"gorm.io/gorm"
	"slices"
)

func lockAuthorization(tx *gorm.DB) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('coinsphere.authorization',0))").Error
}
func userRolesTx(tx *gorm.DB, id int64) ([]db.SystemRole, error) {
	var roles []db.SystemRole
	err := tx.Joins("JOIN user_roles ur ON ur.role_id=roles.id").Where("ur.user_id=?", id).Order("roles.id").Find(&roles).Error
	return roles, err
}
func resolveAssignableRolesTx(tx *gorm.DB, codes []string) ([]db.SystemRole, error) {
	codes = uniqueStrings(codes)
	if len(codes) == 0 {
		codes = []string{"R_USER"}
	}
	var roles []db.SystemRole
	if err := tx.Where("code IN ? AND is_enabled=TRUE", codes).Order("id").Find(&roles).Error; err != nil {
		return nil, err
	}
	if len(roles) != len(codes) {
		return nil, bizErr("存在无效角色")
	}
	for _, r := range roles {
		if r.Code == guestRoleCode {
			return nil, ErrPermission
		}
	}
	return roles, nil
}
func checkRoleGrantCeiling(tx *gorm.DB, p *Principal, roles []db.SystemRole) error {

	for _, r := range roles {
		if r.Code == "R_SUPER" && !p.HasRole("R_SUPER") {
			return ErrPermission
		}
		var permissions []db.Permission
		if err := tx.Joins("JOIN role_permissions rp ON rp.permission_code=permissions.code").Where("rp.role_id=?", r.ID).Find(&permissions).Error; err != nil {
			return err
		}
		for _, permission := range permissions {
			if !p.HasPermission(permission.Code) || permission.Protected && !p.HasRole("R_SUPER") {
				return ErrPermission
			}
		}
	}
	return nil
}
func checkUserMutation(tx *gorm.DB, p *Principal, user db.SystemUser, oldRoles, newRoles []db.SystemRole, active, deleting bool) error {
	old, new := roleCodesOf(oldRoles), roleCodesOf(newRoles)
	slices.Sort(old)
	slices.Sort(new)
	wasSuper := slices.Contains(old, "R_SUPER")
	if (wasSuper || user.Username == protectedSuperUsername) && !p.HasRole("R_SUPER") {
		return ErrPermission
	}
	if (deleting || !active) && p.User.ID == user.ID {
		return bizErr("不能删除或停用当前账号")
	}
	if deleting && user.Username == protectedSuperUsername {
		return ErrPermission
	}
	if !slices.Equal(old, new) {
		if !p.HasPermission("system.users.assign_roles") {
			return ErrPermission
		}
		if err := checkRoleGrantCeiling(tx, p, newRoles); err != nil {
			return err
		}
	}
	if wasSuper && (!active || deleting || !slices.Contains(new, "R_SUPER")) {
		var count int64
		if err := tx.Raw(`SELECT COUNT(*) FROM users u WHERE u.id<>? AND u.is_active AND EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.code='R_SUPER' AND r.is_enabled)`, user.ID).Scan(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("%w: must retain an active administrator", ErrConflict)
		}
	}
	return nil
}
