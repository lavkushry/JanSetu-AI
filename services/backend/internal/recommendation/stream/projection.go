package stream

import (
	"context"
	_ "embed"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Authority struct {
	Generation       int64
	Enabled, Deleted bool
}

func CurrentAuthority(ctx context.Context, db *pgxpool.Pool, subject uuid.UUID) (Authority, error) {
	var a Authority
	err := db.QueryRow(ctx, "SELECT generation,enabled,deleted FROM rec_stream.current_authority($1)", subject).Scan(&a.Generation, &a.Enabled, &a.Deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return Authority{}, nil
	}
	return a, err
}

//go:embed projection.lua
var projectionLua string

type Projection struct {
	Redis     redis.UniversalClient
	Namespace string
}

func (p Projection) Apply(ctx context.Context, e Envelope, a Authority) error {
	if p.Namespace == "" {
		return errors.New("projection namespace required")
	}
	key := p.Namespace + ":{" + e.Key() + "}"
	if e.EventType == "CONTENT" {
		return p.Redis.Eval(ctx, projectionLua, []string{key + ":state", key + ":features", key + ":seen"}, e.EventType, strconv.FormatInt(e.EntityVersion, 10), strconv.FormatInt(int64(e.Revision), 10), strconv.FormatBool(e.Eligible)).Err()
	}
	if a.Generation < 1 {
		return nil
	} // Unknown subject cannot create personal features.
	// No old event can extend behavior beyond the original 30-day retention.
	ttl := int64(time.Until(e.OccurredAt.Add(30 * 24 * time.Hour)).Seconds())
	return p.Redis.Eval(ctx, projectionLua, []string{key + ":state", key + ":features", key + ":seen"},
		e.EventType, strconv.FormatInt(a.Generation, 10), strconv.FormatBool(a.Enabled && !a.Deleted),
		strconv.FormatInt(e.Generation, 10), e.EventID.String(), e.Action, e.PostID.String(), e.NormalizedRead, ttl).Err()
}
