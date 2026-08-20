package api

import (
	"errors"
	"net/http"

	"coinsphere/backend/internal/service"
)

type emergencyStopPayload struct {
	Reason string `json:"reason"`
}

func (s *Server) handleTradingOverview(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetTradingOverview(r.Context(), principal.User.ID)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleListTradingAccounts(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListTradingAccounts(r.Context(), principal.User.ID)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleGetTradingAccount(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetTradingAccountDetail(r.Context(), principal.User.ID, r.PathValue("accountId"))
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleUpdateTradingAccount(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.TradingAccountUpdatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid trading account request")
		return
	}
	data, err := s.App.UpdateTradingAccount(r.Context(), principal.User.ID, r.PathValue("accountId"), *payload)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleArchiveTradingAccount(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	err := s.App.ArchiveTradingAccount(
		r.Context(), principal, r.PathValue("accountId"), r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	if err != nil {
		writeTradingResult(w, r, nil, err)
		return
	}
	ok(w, M{"archived": true})
}

func (s *Server) handleCreateTradingAccount(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.TradingAccountCreatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid trading account request")
		return
	}
	data, err := s.App.CreateTradingAccount(
		r.Context(), principal.User.ID, *payload, r.Header.Get("Idempotency-Key"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleUpdateTradingRisk(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.TradingRiskPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid trading risk request")
		return
	}
	data, err := s.App.UpdateTradingRisk(
		r.Context(), principal, r.PathValue("accountId"), *payload,
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleEnableTradingAutomation(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.SetTradingAutomation(
		r.Context(), principal, r.PathValue("accountId"), true,
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleDisableTradingAutomation(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.SetTradingAutomation(
		r.Context(), principal, r.PathValue("accountId"), false,
		r.Header.Get("Idempotency-Key"), "",
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleResumeTradingAccount(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ResumeTradingAccount(
		r.Context(), principal, r.PathValue("accountId"),
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleSaveTradingCredentials(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.TradingCredentialPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid testnet credential request")
		return
	}
	data, err := s.App.SaveTradingCredentials(
		r.Context(), principal, r.PathValue("accountId"), *payload,
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleRevokeTradingCredentials(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.RevokeTradingCredentials(
		r.Context(), principal, r.PathValue("accountId"),
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleAuthorizeTradingAutomation(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.SetTradingAuthorization(
		r.Context(), principal, r.PathValue("accountId"), true,
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleRevokeTradingAutomation(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.SetTradingAuthorization(
		r.Context(), principal, r.PathValue("accountId"), false,
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleActivateTradingEmergencyStop(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[emergencyStopPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid emergency stop request")
		return
	}
	data, err := s.App.ActivateTradingEmergencyStop(
		r.Context(), principal, payload.Reason, r.Header.Get("Idempotency-Key"),
	)
	writeTradingResult(w, r, data, err)
}

func (s *Server) handleReleaseTradingEmergencyStop(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ReleaseTradingEmergencyStop(
		r.Context(), principal, r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeTradingResult(w, r, data, err)
}

func writeTradingResult(w http.ResponseWriter, r *http.Request, data any, err error) {
	if err == nil {
		ok(w, data)
		return
	}
	status, detail := http.StatusInternalServerError, "trading operation failed"
	switch {
	case errors.Is(err, service.ErrInvalidTradingRequest), errors.Is(err, service.ErrTradingCredentialInvalid):
		status, detail = http.StatusBadRequest, err.Error()
	case errors.Is(err, service.ErrTradingAccountMissing):
		status, detail = http.StatusNotFound, err.Error()
	case errors.Is(err, service.ErrTradingReauthentication):
		status, detail = http.StatusUnauthorized, err.Error()
	case errors.Is(err, service.ErrPermission):
		status, detail = http.StatusForbidden, err.Error()
	case service.IsIdempotencyConflict(err), errors.Is(err, service.ErrTradingAccountConflict),
		errors.Is(err, service.ErrTradingExecutionUnavailable), errors.Is(err, service.ErrTradingCredentialsMissing),
		errors.Is(err, service.ErrTradingCredentialsUnverified), errors.Is(err, service.ErrTradingReconciliationRequired):
		status, detail = http.StatusConflict, err.Error()
	}
	writeProblem(w, r, status, detail)
}
