package service

import "testing"

func TestCursorIsBoundToScopeAndCarriesStableKey(t *testing.T) {
	page, err := ParseCursorPage("", 50, "news?keyword=btc")
	if err != nil {
		t.Fatalf("parse first page: %v", err)
	}
	result := cursorResult([]M{{"id": int64(9)}}, page, "9", true, 2)
	raw, _ := result["nextCursor"].(string)
	if raw == "" {
		t.Fatal("next cursor was not issued")
	}
	next, err := ParseCursorPage(raw, 50, "news?keyword=btc")
	if err != nil || next.After != "9" {
		t.Fatalf("next cursor = %#v, err = %v", next, err)
	}
	if _, err := ParseCursorPage(raw, 50, "news?keyword=eth"); err == nil {
		t.Fatal("cursor crossed its filter scope")
	}
}

func TestCursorRejectsMalformedKey(t *testing.T) {
	page := CursorPage{After: "not-an-id"}
	if _, err := page.AfterID(); err == nil {
		t.Fatal("non-numeric id cursor was accepted")
	}
}
