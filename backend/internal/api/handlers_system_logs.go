package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"coinsphere/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleListSystemLogs(c *gin.Context) {
	page, ok := cursorPage(c)
	if !ok {
		return
	}
	query, err := systemLogQuery(c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	query.Page = page
	data, err := s.App.ListSystemLogs(c.Request.Context(), query)
	respond(c, data, err, "")
}

func (s *Server) handleGetSystemLogRuntime(c *gin.Context) {
	data, err := s.App.GetSystemLogRuntime()
	respond(c, data, err, "")
}

func (s *Server) handleUpdateSystemLogRuntime(c *gin.Context) {
	payload, err := decodeBody[service.SystemLogSettingsPayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.UpdateSystemLogRuntime(c.Request.Context(), *payload, currentPrincipal(c).User.ID)
	respond(c, data, err, "日志配置已应用")
}

func systemLogQuery(c *gin.Context) (service.SystemLogQuery, error) {
	startTime, err := workflowRunQueryTime(c, "startTime")
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	endTime, err := workflowRunQueryTime(c, "endTime")
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	userID, err := systemLogPositiveInt64(c, "userId")
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	statusCode, err := systemLogStatusCode(c)
	if err != nil {
		return service.SystemLogQuery{}, err
	}
	level := strings.ToLower(queryStr(c, "level"))
	if level != "" && level != "debug" && level != "info" && level != "warn" && level != "error" {
		return service.SystemLogQuery{}, errors.New("level must be debug, info, warn, or error")
	}
	method := strings.ToUpper(queryStr(c, "method"))
	if method != "" && !map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true,
		"DELETE": true, "HEAD": true, "OPTIONS": true,
	}[method] {
		return service.SystemLogQuery{}, errors.New("method is invalid")
	}
	component := queryStr(c, "component")
	requestID := queryStr(c, "requestId")
	route := queryStr(c, "route")
	keyword := queryStr(c, "keyword")
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

func systemLogPositiveInt64(c *gin.Context, name string) (*int64, error) {
	raw := queryStr(c, name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil, errors.New(name + " must be a positive integer")
	}
	return &value, nil
}

func systemLogStatusCode(c *gin.Context) (*int, error) {
	raw := queryStr(c, "statusCode")
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
