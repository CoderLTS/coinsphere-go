package service

import "testing"

func TestNormalizeIdempotencyKey(t *testing.T) {
	valid := "  0123456789abcdef  "
	got, err := normalizeIdempotencyKey(valid)
	if err != nil {
		t.Fatalf("normalize valid key: %v", err)
	}
	if got != "0123456789abcdef" {
		t.Fatalf("normalized key = %q", got)
	}

	for _, key := range []string{"", "0123456789abcde", string(make([]byte, 201))} {
		if _, err := normalizeIdempotencyKey(key); err == nil {
			t.Fatalf("expected invalid key %q to fail", key)
		}
	}
}

func TestCanonicalRequestHashIsStableForObjectKeyOrder(t *testing.T) {
	left, err := canonicalRequestHash(M{
		"startEntryKeys": []string{"entry-a"},
		"inputs":         M{"a": float64(1), "b": float64(2)},
	})
	if err != nil {
		t.Fatalf("hash left request: %v", err)
	}
	right, err := canonicalRequestHash(M{
		"inputs":         M{"b": float64(2), "a": float64(1)},
		"startEntryKeys": []string{"entry-a"},
	})
	if err != nil {
		t.Fatalf("hash right request: %v", err)
	}
	if left != right {
		t.Fatalf("equivalent requests hashed differently: %s != %s", left, right)
	}
}

func TestWorkflowExecutionIdempotencyKeyDoesNotUsePublicKey(t *testing.T) {
	got := workflowExecutionIdempotencyKey(42, "manual:7:entry-a")
	want := "idempotency-record:42:manual:7:entry-a"
	if got != want {
		t.Fatalf("derived key = %q, want %q", got, want)
	}
}
