// Package providers: ADR-013/ADR-014 v1 scope. T3 has NO executor path —
// detect+recommend only. T4 is a framework with no default adapter.
package providers

import (
	"context"
	"fmt"

	"memory_pilot/internal/model"
)

type Spec struct {
	Kind        model.ActionKind
	ID          string
	EffectRange [2]int64 // measured, never guessed (ADR-006 provider bar)
	CanaryGated bool
}

type Provider interface {
	Spec() Spec
	Check(ctx context.Context) error // preconditions; no side effects
	Apply(ctx context.Context, budget int64) (Applied, error)
}

type Applied struct {
	Bytes int64
	Note  string
}

// T1a: self-trim — the daemon releases its own caches.
type TrimOwnBuffers struct{ CacheBytes *int64 }

func (p TrimOwnBuffers) Spec() Spec {
	return Spec{Kind: model.ActionTrimOwnBuffers, ID: "t1-trim", EffectRange: [2]int64{8 << 20, 64 << 20}}
}
func (p TrimOwnBuffers) Check(ctx context.Context) error { return nil }
func (p TrimOwnBuffers) Apply(ctx context.Context, budget int64) (Applied, error) {
	if p.CacheBytes == nil || *p.CacheBytes == 0 {
		return Applied{}, fmt.Errorf("nothing to trim")
	}
	freed := *p.CacheBytes
	*p.CacheBytes = 0
	return Applied{Bytes: freed, Note: "own aggregation caches released"}, nil
}

// T1b: ledger rotation.
type RotateLedger interface{ Rotate() error }

type RotateProvider struct{ L RotateLedger }

func (p RotateProvider) Spec() Spec {
	return Spec{Kind: model.ActionRotateOwnLedger, ID: "t1-rotate", EffectRange: [2]int64{0, 0}}
}
func (p RotateProvider) Check(ctx context.Context) error { return nil }
func (p RotateProvider) Apply(ctx context.Context, budget int64) (Applied, error) {
	return Applied{Note: "ledger rotated"}, p.L.Rotate()
}

// T2: DeferLaunch — advisory for `mp run` to defer new demand. Does not free
// memory; prevents new demand from arriving.
type DeferLaunch struct{}

func (DeferLaunch) Spec() Spec {
	return Spec{Kind: model.ActionDeferLaunch, ID: "t2-defer", EffectRange: [2]int64{0, 0}}
}
func (DeferLaunch) Check(ctx context.Context) error { return nil }
func (DeferLaunch) Apply(ctx context.Context, budget int64) (Applied, error) {
	return Applied{Note: "launch deferred until pressure clears"}, nil
}

// T4: cooperative release FRAMEWORK. No default adapter ships (ADR-014);
// an adapter enters only through the v1-scope widening procedure.
type CooperativeAdapter interface {
	Name() string
	Release(ctx context.Context, budget int64) (Applied, error)
}

type CooperativeRelease struct {
	Adapters map[string]CooperativeAdapter // registered adapters only
}

func (p CooperativeRelease) Spec() Spec {
	return Spec{Kind: model.ActionEmitAdvisory, ID: "t4-coop", EffectRange: [2]int64{0, 0}, CanaryGated: true}
}
func (p CooperativeRelease) Check(ctx context.Context) error {
	if len(p.Adapters) == 0 {
		return fmt.Errorf("no registered adapter (T4 has no default adapter, ADR-014)")
	}
	return nil
}
func (p CooperativeRelease) Apply(ctx context.Context, budget int64) (Applied, error) {
	if len(p.Adapters) == 0 {
		return Applied{}, fmt.Errorf("no adapter registered — cannot act")
	}
	var total int64
	for _, a := range p.Adapters {
		ap, err := a.Release(ctx, budget-total)
		if err != nil {
			return Applied{Bytes: total}, err
		}
		total += ap.Bytes
	}
	return Applied{Bytes: total, Note: "cooperative release via registered adapters"}, nil
}

// L2: StopManagedWorkload is NOT a provider — it is the executor's stopOwned
// path (OwnedPid + pre-auth + identity revalidation). This type exists only
// to document the boundary; it has no Apply.
type StopManagedWorkload struct{}

func (StopManagedWorkload) NoApply() {} // compile-time marker: no executor path here

// T5: advisories with stated strength. Never promises recoverability without
// a verified adapter (G-P5 honest-language rule).
type Advisory struct {
	Strength string // "informational" | "recommendation"
	Text     string
}

func RenderAdvisory(a Advisory, adapterVerified bool) string {
	switch {
	case a.Strength == "recommendation" && adapterVerified:
		return fmt.Sprintf("[recommendation] %s (reversible via registered adapter)", a.Text)
	case a.Strength == "recommendation":
		return fmt.Sprintf("[recommendation] %s (no verified adapter: reversibility NOT guaranteed)", a.Text)
	default:
		return fmt.Sprintf("[informational] %s", a.Text)
	}
}
