package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"coinsphere/backend/internal/service"
)

func (s *Server) handleListSystemLogs(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	query, err := systemLogQuery(r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	query.Page = page
	data, err := s.App.ListSystemLogs(r.Context(), query)
	respond(w, data, err, "")
}

func (s *Server) handleGetSystemLogRuntime(w http.ResponseWriter, _ *http.Request, _ *service.Principal) {
	data, err := s.App.GetSystemLogRuntime()
	respond(w, data, err, "")
}

func (s *Server) handleUpdateSystemLogRuntime(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.SystemLogSettingsPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.UpdateSystemLogRuntime(r.Context(), *payload, principal.User.ID)
	respond(w, data, err, "日志配置已应用")
}

func (s *Server) handleDeleteSystemLogs(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	query, err := systemLogQuery(r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.DeleteSystemLogs(r.Context(), query)
	respond(w, data, err, "筛选日志已清理")
}

func systemLogQuery(r *http.Request) (service.SystemLogQuery, error) {
	startTime, err := workflowRunQueryTime(r, "startTime")
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	endTime, err := workflowRunQueryTime(r, "endTime")
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	userID, err := systemLogPositiveInt64(r, "userId")
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	statusCode, err := systemLogStatusCode(r)
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	level := strings.ToLower(queryStr(r, "level"))
	if level != "" && level != "debug" && level != "info" && level != "warn" && level != "error" {
		return service.SystemLogQuery{}, errors.New("level must be debug, info, warn, or error")
	}
	method := strings.ToUpper(queryStr(r, "method"))
	if method != "" && !map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true,
		"DELETE": true, "HEAD": true, "OPTIONS": true,
	}[method] {
		return service.SystemLogQuery{}, errors.New("method is invalid")
	}
	component := queryStr(r, "component")
	requestID := queryStr(r, "requestId")
	route := queryStr(r, "route")
	keyword := queryStr(r, "keyword")
	if len(component) > 64 || len(requestID) > 64 || len(route) > 255 || len(keyword) > 100 {
		return service.SystemLogQuery{}, errors.New("log filter is too long")
	}
	if requestID != "" && !validLogRequestID(requestID) {
		return service.SystemLogQuery{}, errors.New("requestId is invalid")
	}
	if startTime != nil && endTime != nil && startTime.After(*endTime) {
		return service.SystemLogQuery{}, errors.New("startTime must not be after endTime")
	}
	return service.SystemLogQuery{
		StartTime: startTime, EndTime: endTime, Level: level, Component: component,
		RequestID: requestID, UserID: userID, Method: method, Route: route,
		StatusCode: statusCode, Keyword: keyword,
	}, nil
}

func systemLogPositiveInt64(r *http.Request, name string) (*int64, error) {
	raw := queryStr(r, name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil, errors.New(name + " must be a positive integer")
	}
	return &value, nil
}

func systemLogStatusCode(r *http.Request) (*int, error) {
	raw := queryStr(r, "statusCode")
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 100 || value > 599 {
		return nil, errors.New("statusCode must be between 100 and 599")
	}
	return &value, nil
}

func validLogRequestID(value string) bool {
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
