package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListStrategyDrafts(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	if !requireStrategyAdmin(w, r, principal) {
		return
	}
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListStrategyDrafts(r.Context(), page)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleCreateStrategyDraft(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	if !requireStrategyAdmin(w, r, principal) {
		return
	}
	payload, err := decodeStrictBody[service.StrategyDraftPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid strategy draft request")
		return
	}
	data, err := s.App.CreateStrategyDraft(r.Context(), principal.User.ID, *payload)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleGetStrategyDraft(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	if !requireStrategyAdmin(w, r, principal) {
		return
	}
	data, err := s.App.GetStrategyDraft(r.Context(), r.PathValue("strategyId"))
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleUpdateStrategyDraft(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	if !requireStrategyAdmin(w, r, principal) {
		return
	}
	payload, err := decodeStrictBody[service.StrategyDraftPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid strategy draft request")
		return
	}
	data, err := s.App.UpdateStrategyDraft(r.Context(), principal.User.ID, r.PathValue("strategyId"), *payload)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handlePublishStrategy(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	if !requireStrategyAdmin(w, r, principal) {
		return
	}
	data, err := s.App.PublishStrategy(
		r.Context(), principal.User.ID, r.PathValue("strategyId"), r.Header.Get("Idempotency-Key"),
	)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleListPublishedStrategies(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListPublishedStrategies(r.Context(), page)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleGetPublishedStrategy(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.GetPublishedStrategy(r.Context(), r.PathValue("strategyVersionId"))
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleListBacktests(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListBacktests(r.Context(), principal.User.ID, page)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleCreateBacktest(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.BacktestCreatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid backtest request")
		return
	}
	data, err := s.App.CreateBacktest(r.Context(), principal.User.ID, r.Header.Get("Idempotency-Key"), *payload)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleGetBacktest(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetBacktest(r.Context(), principal.User.ID, r.PathValue("backtestId"))
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleCancelBacktest(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.CancelBacktest(r.Context(), principal.User.ID, r.PathValue("backtestId"))
	writeStrategyBacktestResult(w, r, data, err)
}

func requireStrategyAdmin(w http.ResponseWriter, r *http.Request, principal *service.Principal) bool {
	if principal.HasRole("R_SUPER") {
		return true
	}
	writeProblem(w, r, http.StatusForbidden, "permission denied")
	return false
}

func decodeStrictBody[T any](r *http.Request) (*T, error) {
	if r.Body == nil {
		return nil, io.EOF
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var payload T
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body must contain exactly one JSON value")
	}
	return &payload, nil
}

func writeStrategyBacktestResult(w http.ResponseWriter, r *http.Request, data any, err error) {
	if err == nil {
		ok(w, data)
		return
	}
	status, detail := http.StatusInternalServerError, "strategy or backtest operation failed"
	switch {
	case errors.Is(err, service.ErrInvalidStrategyRequest):
		status, detail = http.StatusBadRequest, err.Error()
	case errors.Is(err, service.ErrStrategyDraftMissing),
		errors.Is(err, service.ErrStrategyInstrumentMissing),
		errors.Is(err, service.ErrStrategyVersionMissing),
		errors.Is(err, service.ErrStrategyInstanceMissing),
		errors.Is(err, service.ErrStrategySignalMissing),
		errors.Is(err, service.ErrTradingAccountMissing),
		errors.Is(err, service.ErrBacktestMissing):
		status, detail = http.StatusNotFound, err.Error()
	case errors.Is(err, service.ErrStrategySignalReauthentication):
		status, detail = http.StatusUnauthorized, err.Error()
	case service.IsIdempotencyConflict(err), errors.Is(err, service.ErrBacktestConflict),
		errors.Is(err, service.ErrStrategySignalConflict), errors.Is(err, service.ErrTradingAccountConflict),
		errors.Is(err, service.ErrTradingExecutionUnavailable):
		status, detail = http.StatusConflict, err.Error()
	}
	writeProblem(w, r, status, detail)
}
