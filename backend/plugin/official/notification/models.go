package notification

import "time"

type Delivery struct {
	ID                int64 `gorm:"primaryKey;autoIncrement"`
	OperationKey      string
	WorkflowID        int64
	RevisionID        int64
	NodeInstanceID    string
	Channel           string
	SubjectKey        string
	Title             string
	Message           string
	Status            string
	AttemptCount      int
	DeliveredAt       *time.Time
	LastErrorCategory *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (Delivery) TableName() string { return "plugin_notification.deliveries" }
