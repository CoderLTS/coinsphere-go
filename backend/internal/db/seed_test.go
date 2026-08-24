package db

import (
	"reflect"
	"testing"
)

func TestV2BaselineMenuSeeds(t *testing.T) {
	got := make([]string, 0, len(menuItems))
	for _, item := range menuItems {
		got = append(got, item.Name)
	}
	want := []string{"Home", "System", "User", "Role", "Menus", "UserCenter"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("baseline menus = %v, want %v", got, want)
	}
}
