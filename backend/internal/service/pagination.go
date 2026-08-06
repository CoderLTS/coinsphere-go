package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

// CursorPage is a validated, route-and-filter-bound keyset page request.
type CursorPage struct {
	After string
	Limit int
	scope string
}

type cursorPayload struct {
	Version int    `json:"v"`
	Scope   string `json:"s"`
	Key     string `json:"k"`
}

func ParseCursorPage(raw string, limit int, scope string) (CursorPage, error) {
	page := CursorPage{Limit: limit, scope: scope}
	if raw == "" {
		return page, nil
	}
	if len(raw) > 2048 {
		return CursorPage{}, errors.New("invalid cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return CursorPage{}, errors.New("invalid cursor")
	}
	var payload cursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil || payload.Version != 1 || payload.Scope != scope || payload.Key == "" {
		return CursorPage{}, errors.New("invalid cursor")
	}
	page.After = payload.Key
	return page, nil
}

func (p CursorPage) AfterID() (int64, error) {
	if p.After == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(p.After, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid cursor")
	}
	return id, nil
}

func cursorResult(records []M, page CursorPage, lastKey string, hasMore bool, total int64) M {
	nextCursor := ""
	if hasMore && lastKey != "" {
		raw, _ := json.Marshal(cursorPayload{Version: 1, Scope: page.scope, Key: lastKey})
		nextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return M{
		"records": records, "nextCursor": nextCursor,
		"hasMore": hasMore, "total": total,
	}
}

func int64CursorKey(id int64) string { return strconv.FormatInt(id, 10) }
