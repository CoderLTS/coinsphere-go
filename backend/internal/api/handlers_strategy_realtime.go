package api

import (
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListStrategyInstances(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListStrategyInstances(r.Context(), principal.User.ID, page)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleListStrategySignals(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListStrategySignals(r.Context(), principal.User.ID, service.StrategySignalQuery{
		Page: page, InstrumentID: queryStr(r, "instrumentId"), StrategyInstanceID: queryStr(r, "strategyInstance"),
		Interval: queryStr(r, "interval"), StartTime: queryStr(r, "startTime"), EndTime: queryStr(r, "endTime"),
	})
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleApproveStrategySignal(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.DecideStrategySignal(
		r.Context(), principal, r.PathValue("signalId"), "approved",
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	writeStrategyBacktestResult(w, r, data, err)
}

func (s *Server) handleRejectStrategySignal(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.DecideStrategySignal(
		r.Context(), principal, r.PathValue("signalId"), "rejected",
		r.Header.Get("Idempotency-Key"), "",
	)
	writeStrategyBacktestResult(w, r, data, err)
}
