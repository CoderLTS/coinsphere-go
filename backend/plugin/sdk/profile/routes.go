package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
)

type draftPayload struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Summary string          `json:"summary"`
	Config  json.RawMessage `json:"config"`
}

type publishPayload struct {
	Version string `json:"version"`
}

// RegisterRoutes exposes one plugin-owned profile type under the registry's
// normal /plugins/{pluginId} prefix. The type is part of the path so a plugin
// can register several independent Profile stores without route collisions.
func RegisterRoutes(registrar sdk.Registrar, store *Store) error {
	if registrar == nil || store == nil {
		return ErrNotFound
	}
	// A plugin may own several Profile types.  Scope the route by the
	// descriptor type so each provider can register its own schema and storage
	// without colliding on a shared /profiles path.
	prefix := "/profiles/" + store.descriptor.Type
	routes := []struct {
		desc    sdk.RouteDescriptor
		handler sdk.ScopedRouteHandler
	}{
		{sdk.RouteDescriptor{Method: http.MethodGet, Pattern: prefix + "/descriptor", Scope: sdk.ScopeSystem}, store.handleDescriptor},
		{sdk.RouteDescriptor{Method: http.MethodGet, Pattern: prefix, Scope: sdk.ScopeSystem}, store.handleList},
		{sdk.RouteDescriptor{Method: http.MethodGet, Pattern: prefix + "/:profileId", Scope: sdk.ScopeSystem}, store.handleGet},
		{sdk.RouteDescriptor{Method: http.MethodPost, Pattern: prefix, Scope: sdk.ScopeSystem}, store.handleCreate},
		{sdk.RouteDescriptor{Method: http.MethodPut, Pattern: prefix + "/:profileId", Scope: sdk.ScopeSystem}, store.handleUpdate},
		{sdk.RouteDescriptor{Method: http.MethodPost, Pattern: prefix + "/:profileId/copy", Scope: sdk.ScopeSystem}, store.handleCopy},
		{sdk.RouteDescriptor{Method: http.MethodPost, Pattern: prefix + "/:profileId/publish", Scope: sdk.ScopeSystem}, store.handlePublish},
		{sdk.RouteDescriptor{Method: http.MethodPost, Pattern: prefix + "/:profileId/disable", Scope: sdk.ScopeSystem}, store.handleDisable},
		{sdk.RouteDescriptor{Method: http.MethodDelete, Pattern: prefix + "/:profileId", Scope: sdk.ScopeSystem}, store.handleDelete},
	}
	for _, route := range routes {
		if err := registrar.Route(route.desc, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) handleDescriptor(c *gin.Context, scope sdk.RouteScope) {
	if _, ok := profileActor(scope, s.pluginID); !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	descriptor := s.descriptor
	writeProfileJSON(c, http.StatusOK, gin.H{"item": sdk.ProfileDescriptor{
		Type: descriptor.Type, Version: descriptor.Version, Title: descriptor.Title,
		Description: descriptor.Description, ConfigSchema: descriptor.ConfigSchema, UISchema: descriptor.UISchema,
	}})
}

func (s *Store) handleGet(c *gin.Context, scope sdk.RouteScope) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	if err := s.authorizeCall(c.Request.Context(), actor, sdk.ProfilePermissionRead); err != nil {
		writeProfileError(c, http.StatusForbidden, err.Error())
		return
	}
	item, err := s.Get(c.Request.Context(), c.Param("profileId"), c.Query("includeDisabled") == "true")
	if err != nil {
		writeProfileError(c, profileErrorStatus(err), err.Error())
		return
	}
	writeProfileJSON(c, http.StatusOK, gin.H{"item": item})
}

func (s *Store) handleList(c *gin.Context, scope sdk.RouteScope) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	if err := s.authorizeCall(c.Request.Context(), actor, sdk.ProfilePermissionRead); err != nil {
		writeProfileError(c, http.StatusForbidden, err.Error())
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	items, err := s.List(c.Request.Context(), sdk.ProfileListQuery{Type: c.Query("type"), IncludeDisabled: c.Query("includeDisabled") == "true", Limit: limit})
	if err != nil {
		writeProfileError(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeProfileJSON(c, http.StatusOK, gin.H{"items": items})
}

func (s *Store) handleCreate(c *gin.Context, scope sdk.RouteScope) {
	s.handleDraft(c, scope, "create")
}

func (s *Store) handleUpdate(c *gin.Context, scope sdk.RouteScope) {
	s.handleDraft(c, scope, "update")
}

func (s *Store) handleDraft(c *gin.Context, scope sdk.RouteScope, operation string) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	var payload draftPayload
	if c.ShouldBindJSON(&payload) != nil {
		writeProfileError(c, http.StatusBadRequest, "invalid profile payload")
		return
	}
	if operation == "update" {
		payload.ID = c.Param("profileId")
	}
	draft := Draft(payload)
	var item sdk.ProfileSummary
	var err error
	if operation == "create" {
		item, err = s.Create(c.Request.Context(), actor, draft)
	} else {
		item, err = s.Update(c.Request.Context(), actor, draft)
	}
	if err != nil {
		writeProfileError(c, profileErrorStatus(err), err.Error())
		return
	}
	writeProfileJSON(c, http.StatusOK, item)
}

func (s *Store) handleCopy(c *gin.Context, scope sdk.RouteScope) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	var payload draftPayload
	if c.Request.ContentLength != 0 && c.ShouldBindJSON(&payload) != nil {
		writeProfileError(c, http.StatusBadRequest, "invalid profile payload")
		return
	}
	item, err := s.Copy(c.Request.Context(), actor, c.Param("profileId"), Draft(payload))
	if err != nil {
		writeProfileError(c, profileErrorStatus(err), err.Error())
		return
	}
	writeProfileJSON(c, http.StatusOK, item)
}

func (s *Store) handlePublish(c *gin.Context, scope sdk.RouteScope) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	var payload publishPayload
	if c.ShouldBindJSON(&payload) != nil || strings.TrimSpace(payload.Version) == "" {
		writeProfileError(c, http.StatusBadRequest, "profile version is required")
		return
	}
	item, err := s.Publish(c.Request.Context(), actor, c.Param("profileId"), payload.Version)
	if err != nil {
		writeProfileError(c, profileErrorStatus(err), err.Error())
		return
	}
	writeProfileJSON(c, http.StatusOK, item)
}

func (s *Store) handleDisable(c *gin.Context, scope sdk.RouteScope) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	if err := s.Disable(c.Request.Context(), actor, c.Param("profileId")); err != nil {
		writeProfileError(c, profileErrorStatus(err), err.Error())
		return
	}
	writeProfileJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (s *Store) handleDelete(c *gin.Context, scope sdk.RouteScope) {
	actor, ok := profileActor(scope, s.pluginID)
	if !ok {
		writeProfileError(c, http.StatusForbidden, "invalid profile scope")
		return
	}
	if err := s.Delete(c.Request.Context(), actor, c.Param("profileId")); err != nil {
		writeProfileError(c, profileErrorStatus(err), err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func profileActor(scope sdk.RouteScope, pluginID string) (sdk.ProfileActor, bool) {
	value, ok := scope.(sdk.SystemScope)
	if !ok || value.PluginID != pluginID {
		return sdk.ProfileActor{}, false
	}
	return sdk.ProfileActor{UserID: value.UserID, RoleCodes: append([]string(nil), value.RoleCodes...)}, true
}

func profileErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrVersionMissing):
		return http.StatusNotFound
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrDisabled):
		return http.StatusGone
	default:
		return http.StatusBadRequest
	}
}

func writeProfileJSON(c *gin.Context, status int, payload any) {
	c.JSON(status, gin.H{"code": status, "msg": "success", "data": payload})
}

func writeProfileError(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, gin.H{"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail})
}
