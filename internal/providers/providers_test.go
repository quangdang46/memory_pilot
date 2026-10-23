package providers

import (
	"context"
	"strings"
	"testing"
)

// ST-23: T4 cooperative boundaries — no default adapter, refuse without one,
// no side effects on Check.
func TestST23CooperativeBoundaries(t *testing.T) {
	p := CooperativeRelease{Adapters: map[string]CooperativeAdapter{}}
	if err := p.Check(context.Background()); err == nil {
		t.Fatal("Check must fail with no adapters")
	}
	if _, err := p.Apply(context.Background(), 1e9); err == nil || !strings.Contains(err.Error(), "no adapter") {
		t.Fatalf("Apply without adapter must refuse: %v", err)
	}
}
