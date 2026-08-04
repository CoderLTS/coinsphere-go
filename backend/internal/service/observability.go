package service

import (
	"context"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

// AuditRecordInput 只接收固定请求元数据；调用方不得传入 Header、查询串、正文或错误正文。
type AuditRecordInput struct {
	RequestID    string
	ActorUserID  *int64
	Action       string
	ResourcePath string
	Outcome      string
	StatusCode   int
}

// RecordAudit 在业务动作完成后使用独立短事务持久化结果，失败不改变已提交的业务语义。
// ponytail: 旧管理命令暂不做同事务审计；监管或交易命令需要时在对应领域事务内写审计。
func (a *App) RecordAudit(ctx context.Context, input AuditRecordInput) error {
	return a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(&db.AuditRecord{
			RequestID: input.RequestID, ActorUserID: input.ActorUserID,
			Action: input.Action, ResourcePath: input.ResourcePath,
			Outcome: input.Outcome, StatusCode: input.StatusCode,
		}).Error
	})
}

// DatabaseReady 只验证现有连接池能否访问 PostgreSQL，不执行 DDL 或业务查询。
func (a *App) DatabaseReady(ctx context.Context) error {
	database := a.database
	if database == nil {
		database = a.DB
	}
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
