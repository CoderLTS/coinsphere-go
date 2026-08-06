package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"coinsphere/backend/internal/db"
)

const (
	idempotencyKeyMinLength = 16
	idempotencyKeyMaxLength = 200
	idempotencyRecordTTL    = 24 * time.Hour
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used with a different request")

// IsIdempotencyConflict 供 HTTP 边界将同键不同请求映射为契约要求的 409。
func IsIdempotencyConflict(err error) bool { return errors.Is(err, ErrIdempotencyConflict) }

func normalizeIdempotencyKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < idempotencyKeyMinLength || len(value) > idempotencyKeyMaxLength {
		return "", bizErr("Idempotency-Key must be between %d and %d characters", idempotencyKeyMinLength, idempotencyKeyMaxLength)
	}
	return value, nil
}

func canonicalRequestHash(value any) (string, error) {
	// encoding/json 会排序 map 键，使等价的已解码 JSON 对象得到相同摘要。
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize idempotency request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func hashIdempotencyKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func workflowExecutionIdempotencyKey(recordID int64, entryMarker string) string {
	return fmt.Sprintf("idempotency-record:%d:%s", recordID, entryMarker)
}

func (a *App) reserveIdempotencyRecord(database *gorm.DB, userID int64, scope, key, requestHash string) (db.IdempotencyRecord, bool, error) {
	normalizedKey, err := normalizeIdempotencyKey(key)
	if err != nil {
		return db.IdempotencyRecord{}, false, err
	}
	keyHash := hashIdempotencyKey(normalizedKey)
	now := time.Now().UTC()

	for attempt := 0; attempt < 3; attempt++ {
		var existing db.IdempotencyRecord
		err = database.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND scope = ? AND key_hash = ?", userID, scope, keyHash).
			First(&existing).Error
		switch {
		case err == nil:
			if existing.ExpiresAt.After(now) {
				if existing.RequestHash != requestHash {
					return db.IdempotencyRecord{}, false, ErrIdempotencyConflict
				}
				return existing, true, nil
			}
			if err := database.Delete(&existing).Error; err != nil {
				return db.IdempotencyRecord{}, false, err
			}
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return db.IdempotencyRecord{}, false, err
		default:
			record := db.IdempotencyRecord{
				UserID:      userID,
				Scope:       scope,
				KeyHash:     keyHash,
				RequestHash: requestHash,
				ExpiresAt:   now.Add(idempotencyRecordTTL),
				CreatedAt:   now,
			}
			result := database.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "scope"}, {Name: "key_hash"}},
				DoNothing: true,
			}).Create(&record)
			if result.Error != nil {
				return db.IdempotencyRecord{}, false, result.Error
			}
			if result.RowsAffected == 1 {
				return record, false, nil
			}
		}
	}

	return db.IdempotencyRecord{}, false, errors.New("idempotency record contention")
}
