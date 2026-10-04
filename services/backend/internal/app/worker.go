package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

// ProjectOnce claims and fences one durable event. Projection, deduplication,
// and acknowledgement commit together; duplicate delivery is safe.
func (a *App) ProjectOnce(ctx context.Context, owner string) (bool, error) {
	q := dbgen.New(a.Worker)
	token := uuid.New()
	leaseOwner := pgtype.Text{String: owner, Valid: true}
	event, e := q.ClaimEvent(ctx, dbgen.ClaimEventParams{LeaseOwner: leaseOwner, LeaseToken: &token})
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	e = pgx.BeginTxFunc(ctx, a.Worker, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		claimed, e := q.LockClaim(ctx, dbgen.LockClaimParams{ID: event.ID, LeaseToken: &token, LeaseOwner: leaseOwner})
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		_, e = q.EventProcessed(ctx, event.ID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil && claimed.AggregateType == "POST" {
			// Lock the aggregate before taking the count snapshot so two workers
			// cannot publish counts computed on opposite sides of a mutation.
			if _, e = q.LockPost(ctx, claimed.AggregateID); e != nil {
				return e
			}
			if e = q.RebuildPostStats(ctx, claimed.AggregateID); e != nil {
				return e
			}
		}
		return q.CompleteEvent(ctx, dbgen.CompleteEventParams{ID: event.ID, LeaseToken: &token, LeaseOwner: leaseOwner})
	})
	if e != nil {
		_ = q.RetryEvent(ctx, dbgen.RetryEventParams{ID: event.ID, LeaseToken: &token})
	}
	return true, e
}
func (a *App) RunWorker(ctx context.Context) error {
	owner := uuid.NewString()
	for {
		worked, e := a.ProjectOnce(ctx, owner)
		if e != nil && ctx.Err() == nil {
			return e
		}
		if ctx.Err() != nil {
			return nil
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}
