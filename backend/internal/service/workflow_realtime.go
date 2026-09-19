package service

// WorkflowRunUpdate is the small event sent to live workflow monitors. The
// detail remains authoritative in PostgreSQL; clients fetch it only after an
// update arrives.
type WorkflowRunUpdate struct {
	WorkflowID int64 `json:"workflowId"`
	RunID      int64 `json:"runId"`
}

// SubscribeWorkflowRuns registers one live monitor for a workflow.
func (a *App) SubscribeWorkflowRuns(workflowID int64) (<-chan WorkflowRunUpdate, func()) {
	updates := make(chan WorkflowRunUpdate, 64)
	a.workflowWatchMu.Lock()
	if a.workflowWatchers == nil {
		a.workflowWatchers = map[int64]map[chan WorkflowRunUpdate]struct{}{}
	}
	watchers := a.workflowWatchers[workflowID]
	if watchers == nil {
		watchers = map[chan WorkflowRunUpdate]struct{}{}
		a.workflowWatchers[workflowID] = watchers
	}
	watchers[updates] = struct{}{}
	a.workflowWatchMu.Unlock()

	var once bool
	return updates, func() {
		if once {
			return
		}
		once = true
		a.workflowWatchMu.Lock()
		if watchers := a.workflowWatchers[workflowID]; watchers != nil {
			delete(watchers, updates)
			if len(watchers) == 0 {
				delete(a.workflowWatchers, workflowID)
			}
		}
		a.workflowWatchMu.Unlock()
	}
}

// PublishWorkflowRunUpdated coalesces bursts for a monitor. A subsequent
// database read always returns the latest complete run detail.
func (a *App) PublishWorkflowRunUpdated(workflowID, runID int64) {
	if workflowID <= 0 || runID <= 0 {
		return
	}
	update := WorkflowRunUpdate{WorkflowID: workflowID, RunID: runID}
	a.workflowWatchMu.RLock()
	defer a.workflowWatchMu.RUnlock()
	for updates := range a.workflowWatchers[workflowID] {
		select {
		case updates <- update:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- update:
			default:
			}
		}
	}
}

// CloseWorkflowEvents releases websocket subscribers and asks in-flight runs
// to stop when the application is shutting down.
func (a *App) CloseWorkflowEvents() {
	a.workflowWatchMu.Lock()
	for workflowID, watchers := range a.workflowWatchers {
		for updates := range watchers {
			close(updates)
		}
		delete(a.workflowWatchers, workflowID)
	}
	a.workflowWatchMu.Unlock()

	a.runCancelMu.Lock()
	for runID, cancel := range a.runCancels {
		if cancel != nil {
			cancel()
		}
		delete(a.runCancels, runID)
	}
	a.runCancelMu.Unlock()
}
