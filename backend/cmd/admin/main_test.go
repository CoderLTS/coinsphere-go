package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadConfirmedPassword(t *testing.T) {
	password := "test-only-strong-password"
	got, err := readConfirmedPassword(strings.NewReader(password+"\n"+password+"\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("readConfirmedPassword: %v", err)
	}
	if got != password {
		t.Fatal("readConfirmedPassword returned a different password")
	}
}

func TestReadConfirmedPasswordRejectsMismatchAndShortInput(t *testing.T) {
	for _, input := range []string{
		"test-only-strong-password\ndifferent-test-password\n",
		"short\nshort\n",
	} {
		if _, err := readConfirmedPassword(strings.NewReader(input), &bytes.Buffer{}); err == nil {
			t.Fatal("readConfirmedPassword accepted invalid input")
		}
	}
}
