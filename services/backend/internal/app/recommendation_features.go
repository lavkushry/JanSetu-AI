package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/features"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

type featureParityResult struct {
	Status           string
	Expected, Actual int
	features.Report
}

// The shadow sample is taken after feed commit. It cannot change ranked scores,
// permissions, exclusions, exposures or cursor order, and holds no locks in Redis.
func (a *App) recommendationFeatureParity(ctx context.Context, viewer uuid.UUID, generation int64, refs []features.Reference) featureParityResult {
	return a.compareRecommendationFeatures(ctx, a.FeatureShadow, viewer, generation, refs)
}

func (a *App) compareRecommendationFeatures(ctx context.Context, reader features.Reader, viewer uuid.UUID, generation int64, refs []features.Reference) featureParityResult {
	result := featureParityResult{Status: "skipped"}
	if reader == nil || viewer == uuid.Nil || len(refs) == 0 || len(refs) > features.MaxReferences {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
	defer cancel()
	tx, err := a.begin(ctx)
	if err != nil {
		result.Status = "ledger_unavailable"
		return result
	}
	defer tx.Rollback(ctx)
	var subject uuid.UUID
	var asOf time.Time
	err = tx.QueryRow(ctx, `SELECT p.stream_subject,clock_timestamp() FROM social.recommendation_preference p JOIN social.profile v ON v.id=p.profile_id WHERE p.profile_id=$1 AND p.generation=$2 AND p.personalization_enabled AND v.state='ACTIVE'`, viewer, generation).Scan(&subject, &asOf)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			result.Status = "ledger_unavailable"
		}
		return result
	}
	ids := []uuid.UUID{}
	for _, ref := range refs {
		ids = append(ids, ref.PostID)
	}
	data, err := dbgen.New(tx).RecommendationPosts(ctx, dbgen.RecommendationPostsParams{ViewerID: viewer, PostIds: ids})
	if err != nil {
		result.Status = "ledger_unavailable"
		return result
	}
	live := map[uuid.UUID]int32{}
	for _, p := range data {
		live[p.ID] = p.PublishedRevision
	}
	ids = []uuid.UUID{}
	revisions := []int32{}
	eligible := []features.Reference{}
	for _, ref := range refs {
		if live[ref.PostID] == ref.Revision && ref.Revision > 0 {
			ids = append(ids, ref.PostID)
			revisions = append(revisions, ref.Revision)
			eligible = append(eligible, ref)
		}
	}
	if len(eligible) == 0 {
		return result
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (x.post_id,x.revision,e.kind) e.id,x.post_id,x.revision,e.kind,e.created_at,e.normalized_read
 FROM social.recommendation_event e JOIN social.recommendation_exposure x ON x.id=e.exposure_id
 JOIN unnest($3::uuid[],$4::integer[]) wanted(post_id,revision) ON wanted.post_id=x.post_id AND wanted.revision=x.revision
 WHERE e.profile_id=$1 AND e.generation=$2 AND e.created_at>$5::timestamptz-interval '30 days' AND e.created_at<=$5
 ORDER BY x.post_id,x.revision,e.kind,e.created_at DESC,e.id DESC`, viewer, generation, ids, revisions, asOf)
	if err != nil {
		result.Status = "ledger_unavailable"
		return result
	}
	expected := []features.Observation{}
	for rows.Next() {
		var o features.Observation
		if err = rows.Scan(&o.EventID, &o.PostID, &o.Revision, &o.Action, &o.OccurredAt, &o.NormalizedRead); err != nil {
			break
		}
		o.Order = o.OrderKey()
		expected = append(expected, o)
	}
	rows.Close()
	if err != nil || rows.Err() != nil || tx.Commit(ctx) != nil {
		result.Status = "ledger_unavailable"
		return result
	}
	actual, cacheErr := reader.Load(ctx, features.Scope{Subject: subject, Generation: generation, Enabled: true, AsOf: asOf}, eligible)
	// Discard the comparison if consent/reset/deactivation changed while Redis ran.
	var current bool
	err = a.store(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT FROM social.recommendation_preference p JOIN social.profile v ON v.id=p.profile_id WHERE p.profile_id=$1 AND p.generation=$2 AND p.personalization_enabled AND v.state='ACTIVE')`, viewer, generation).Scan(&current)
	if err != nil {
		result.Status = "authority_unavailable"
		return result
	}
	if !current {
		result.Status = "generation_changed"
		return result
	}
	result.Expected = len(expected)
	if cacheErr != nil {
		result.Status = "projection_unavailable"
		return result
	}
	result.Actual = len(actual)
	result.Report = features.Compare(expected, actual)
	result.Status = "compared"
	return result
}

func (a *App) observeRecommendationFeatures(ctx context.Context, reader features.Reader, viewer uuid.UUID, generation int64, refs []features.Reference) {
	start := time.Now()
	r := a.compareRecommendationFeatures(ctx, reader, viewer, generation, refs)
	slog.Info("recommendation feature parity", "status", r.Status, "expected", r.Expected, "actual", r.Actual,
		"matched", r.Matched, "missing", r.Missing, "extra", r.Extra, "different", r.Different, "elapsedMs", time.Since(start).Milliseconds())
}

// Optional shadow work never waits for capacity or extends the feed response.
// Capture immutable input and preserve signed scope across request cancellation.
func (a *App) scheduleRecommendationFeatures(ctx context.Context, viewer uuid.UUID, generation int64, refs []features.Reference) {
	a.featureParityMu.Lock()
	defer a.featureParityMu.Unlock()
	reader := a.FeatureShadow
	if a.featureParityClosing || reader == nil || viewer == uuid.Nil || len(refs) == 0 || len(refs) > features.MaxReferences {
		return
	}
	select {
	case a.featureParitySlots <- struct{}{}:
		refs = append([]features.Reference{}, refs...)
		a.featureParityJobs.Add(1)
		go func() {
			defer a.featureParityJobs.Done()
			defer func() { <-a.featureParitySlots }()
			a.observeRecommendationFeatures(context.WithoutCancel(ctx), reader, viewer, generation, refs)
		}()
	default:
		slog.Info("recommendation feature parity", "status", "sample_dropped")
	}
}

// CloseFeatureShadow stops admission, drains bounded jobs, then closes Redis.
func (a *App) CloseFeatureShadow() error {
	a.featureParityMu.Lock()
	a.featureParityClosing = true
	reader := a.FeatureShadow
	a.featureParityMu.Unlock()
	a.featureParityJobs.Wait()
	if reader != nil {
		return reader.Close()
	}
	return nil
}
