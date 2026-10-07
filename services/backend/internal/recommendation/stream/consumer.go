package stream

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ProjectRecord deliberately refuses unknown envelopes. Poison records stop
// processing without acknowledging them or copying payloads into diagnostics.
func ProjectRecord(ctx context.Context, db *pgxpool.Pool, projection Projection, record *kgo.Record) error {
	e, err := Decode(record.Value)
	if err != nil || string(record.Key) != e.Key() {
		return errors.New("invalid recommendation stream record")
	}
	var a Authority
	if e.EventType != "CONTENT" {
		a, err = CurrentAuthority(ctx, db, e.Subject)
		if err != nil {
			return err
		}
	}
	return projection.Apply(ctx, e, a)
}

func Consume(ctx context.Context, client *kgo.Client, db *pgxpool.Pool, projection Projection) error {
	for ctx.Err() == nil {
		fetches := client.PollRecords(ctx, 20)
		if err := fetches.Err(); err != nil {
			client.AllowRebalance()
			return err
		}
		batch, cancel := context.WithTimeout(ctx, 5*time.Second)
		var failed error
		fetches.EachRecord(func(r *kgo.Record) {
			if failed == nil {
				failed = ProjectRecord(batch, db, projection, r)
			}
		})
		if failed == nil && fetches.NumRecords() > 0 {
			failed = client.CommitUncommittedOffsets(batch)
		}
		cancel()
		client.AllowRebalance()
		if failed != nil {
			return failed
		}
	}
	return ctx.Err()
}
