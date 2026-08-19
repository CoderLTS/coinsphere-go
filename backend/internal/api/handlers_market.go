package api

import (
	"errors"
	"net/http"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListMarketSymbols(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListMarketSymbols(r.Context(), service.MarketSymbolQuery{
		Page: page, Market: queryStr(r, "market"), QuoteAsset: queryStr(r, "quoteAsset"),
		Status: queryStr(r, "status"), Keyword: queryStr(r, "keyword"),
	})
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleGetMarketSyncSettings(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.GetMarketSyncSettings(r.Context())
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleUpdateMarketSyncSettings(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeStrictBody[service.MarketSyncSettingsPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid market sync settings")
		return
	}
	data, err := s.App.UpdateMarketSyncSettings(r.Context(), principal.User.ID, *payload)
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleGetMarketSyncStatus(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.GetMarketSyncStatus(r.Context())
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleRunMarketSync(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.RunMarketMetadataSync(r.Context(), principal.User.ID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeMarketResult(w, r, nil, err)
		return
	}
	writeJSON(w, http.StatusAccepted, M{"code": 200, "msg": "同步工作流已加入执行队列", "data": data})
}

func (s *Server) handleCheckMarketProxy(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.CheckMarketProxy(r.Context())
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleListMarketCandles(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListMarketCandles(r.Context(), service.CandleListQuery{
		Page: page, InstrumentID: queryStr(r, "instrumentId"), Interval: queryStr(r, "interval"),
		StartTime: queryStr(r, "startTime"), EndTime: queryStr(r, "endTime"),
	})
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleListWatchlists(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, valid := cursorPage(w, r)
	if !valid {
		return
	}
	data, err := s.App.ListWatchlistItems(r.Context(), principal.User.ID, page)
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleCreateWatchlist(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.WatchlistCreatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid watchlist request")
		return
	}
	data, err := s.App.CreateWatchlistItem(r.Context(), principal.User.ID, *payload)
	writeMarketResult(w, r, data, err)
}

func (s *Server) handleDeleteWatchlist(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	err := s.App.DeleteWatchlistItem(r.Context(), principal.User.ID, r.PathValue("watchlistId"))
	writeMarketResult(w, r, M{}, err)
}

func writeMarketResult(w http.ResponseWriter, r *http.Request, data any, err error) {
	if err == nil {
		ok(w, data)
		return
	}
	status, detail := http.StatusInternalServerError, "market operation failed"
	switch {
	case errors.Is(err, service.ErrInvalidMarketRequest):
		status, detail = http.StatusBadRequest, err.Error()
	case errors.Is(err, service.ErrMarketResourceMissing):
		status, detail = http.StatusNotFound, service.ErrMarketResourceMissing.Error()
	case errors.Is(err, service.ErrWatchlistExists):
		status, detail = http.StatusConflict, service.ErrWatchlistExists.Error()
	case service.IsIdempotencyConflict(err):
		status, detail = http.StatusConflict, err.Error()
	}
	writeProblem(w, r, status, detail)
}
