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
		Page: page, Market: queryStr(r, "market"), Keyword: queryStr(r, "keyword"),
	})
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
	}
	writeProblem(w, r, status, detail)
}
