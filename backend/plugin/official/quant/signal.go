package quant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type quantSignalAction struct{ runtime *quantRuntime }

func (a quantSignalAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantSeriesConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		StrategyID, StrategyVersion, Target, EvaluatedAt, BusinessKey string
	}
	if json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("quant signal input is invalid")
	}
	target, targetErr := decimal.NewFromString(input.Target)
	evaluatedAt, timeErr := parseQuantUTCTime(input.EvaluatedAt)
	workflowID, workflowErr := quantInt64(request.Revision.WorkflowID)
	revisionID, revisionErr := quantInt64(request.Revision.RevisionID)
	input.StrategyID = strings.TrimSpace(input.StrategyID)
	input.StrategyVersion = strings.TrimSpace(input.StrategyVersion)
	input.BusinessKey = strings.TrimSpace(input.BusinessKey)
	if targetErr != nil || timeErr != nil || workflowErr != nil || revisionErr != nil ||
		target.LessThan(decimal.NewFromInt(-1)) || target.GreaterThan(quantOne) || input.StrategyID == "" ||
		input.StrategyVersion == "" || input.BusinessKey == "" || len(input.BusinessKey) > 256 {
		return sdk.ActionResult{}, errors.New("quant signal identity or Decimal target is invalid")
	}
	if existing, ok, err := a.runtime.loadQuantSignalByOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return quantSignalResult(existing), nil
	}
	now := time.Now().UTC()
	row := quantSignal{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID,
		NodeInstanceID: request.NodeInstanceID, StrategyID: input.StrategyID, StrategyVersion: input.StrategyVersion,
		Venue: config.Venue, Market: config.Market, Instrument: config.Instrument, BusinessKey: input.BusinessKey,
		Target: target, EvaluatedAt: evaluatedAt, Status: "pending", CreatedAt: now,
	}
	err = a.runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		identity := fmt.Sprintf("%d:%s:%s", workflowID, request.NodeInstanceID, input.BusinessKey)
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, identity).Error; err != nil {
			return errors.New("lock Quant signal business identity failed")
		}
		var superseded []quantSignal
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"workflow_id = ? AND node_instance_id = ? AND business_key = ? AND status = 'pending'",
			workflowID, request.NodeInstanceID, input.BusinessKey,
		).Find(&superseded).Error; err != nil {
			return errors.New("load replaced Quant signals failed")
		}
		if len(superseded) > 0 {
			if err := tx.Model(&quantSignal{}).Where("id IN ?", quantSignalIDs(superseded)).Updates(map[string]any{
				"status": "superseded", "decided_at": now,
			}).Error; err != nil {
				return errors.New("replace pending Quant signals failed")
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			return errors.New("persist Quant signal failed")
		}
		if len(superseded) > 0 {
			if err := tx.Model(&quantSignal{}).Where("id IN ?", quantSignalIDs(superseded)).Update("superseded_by", row.ID).Error; err != nil {
				return errors.New("link replaced Quant signals failed")
			}
		}
		return nil
	})
	if err != nil {
		if existing, ok, loadErr := a.runtime.loadQuantSignalByOperation(ctx, request.OperationKey); loadErr == nil && ok {
			return quantSignalResult(existing), nil
		}
		return sdk.ActionResult{}, err
	}
	return quantSignalResult(row), nil
}

func (q *quantRuntime) loadQuantSignalByOperation(ctx context.Context, operationKey string) (quantSignal, bool, error) {
	var signal quantSignal
	if err := q.db.WithContext(ctx).Where("operation_key = ?", operationKey).First(&signal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quantSignal{}, false, nil
		}
		return quantSignal{}, false, errors.New("load Quant signal failed")
	}
	return signal, true, nil
}

func quantSignalResult(signal quantSignal) sdk.ActionResult {
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"signalId": signal.ID, "venue": signal.Venue, "businessKey": signal.BusinessKey, "target": signal.Target.String(), "status": signal.Status,
	})}
}

func quantSignalIDs(signals []quantSignal) []int64 {
	ids := make([]int64, len(signals))
	for index := range signals {
		ids[index] = signals[index].ID
	}
	return ids
}

var _ sdk.ActionHandler = quantSignalAction{}
