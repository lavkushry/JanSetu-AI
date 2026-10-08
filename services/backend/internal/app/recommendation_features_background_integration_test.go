package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/features"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

func TestRecommendationFeatureBackgroundAdmissionScopeAndDrain(t *testing.T) {
	a, _ := featureProofApp(t)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	ids := recommendationFixture(t, a)
	p := consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	refs := []features.Reference{{PostID: ids[0], Revision: 1}}
	reader := a.FeatureShadow
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	a.FeatureShadow = featureReadHook{Reader: reader, load: func(ctx context.Context, s features.Scope, refs []features.Reference) ([]features.Observation, error) {
		if ctx.Err() != nil || s.Generation != p.Generation || !s.Enabled || len(refs) != 1 || refs[0].PostID != ids[0] {
			t.Error("background job lost scope or immutable references")
		}
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return reader.Load(ctx, s, refs)
	}}
	ctx, cancel := context.WithCancel(scopedContext(owner, a.DB, vault.Grant{}))
	cancel() // HTTP cancellation must not remove the signed owner scope.
	for i := 0; i < 2; i++ {
		input := append([]features.Reference{}, refs...)
		a.scheduleRecommendationFeatures(ctx, viewer, p.Generation, input)
		input[0].PostID = uuid.Nil // Admission must have copied caller-owned input.
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("background job failed to read the authorized ledger")
		}
	}
	// Both slots are blocked. Admission must return without waiting or spawning
	// a third reader, and later mutation must not alter either accepted sample.
	a.scheduleRecommendationFeatures(ctx, viewer, p.Generation, refs)
	if calls.Load() != 2 || len(a.featureParitySlots) != 2 {
		t.Fatal("shadow sampling exceeded its admission bound", calls.Load())
	}
	close(release)
	_ = a.CloseFeatureShadow()
	if len(a.featureParitySlots) != 0 {
		t.Fatal("shutdown did not drain shadow jobs")
	}
	a.scheduleRecommendationFeatures(ctx, viewer, p.Generation, []features.Reference{{PostID: ids[0], Revision: 1}})
	if calls.Load() != 2 {
		t.Fatal("shadow reader admitted work after shutdown")
	}
}
