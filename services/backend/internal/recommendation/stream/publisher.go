package stream

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

type Producer interface {
	Publish(context.Context, string, []byte) error
}

type KafkaProducer struct{ Client *kgo.Client }

func (p KafkaProducer) Publish(ctx context.Context, key string, data []byte) error {
	return p.Client.ProduceSync(ctx, &kgo.Record{Topic: Topic, Key: []byte(key), Value: data}).FirstErr()
}

// PublishBatch holds no database transaction across a network call. Leases and
// event identities make ambiguous acknowledgements safe to replay.
func PublishBatch(ctx context.Context, db *pgxpool.Pool, producer Producer) (int, error) {
	token := uuid.New()
	rows, err := db.Query(ctx, "SELECT id,aggregate_key,payload FROM rec_stream.claim($1,20)", token)
	if err != nil {
		return 0, err
	}
	type item struct {
		id   uuid.UUID
		key  string
		data []byte
	}
	var items []item
	for rows.Next() {
		var item item
		if err = rows.Scan(&item.id, &item.key, &item.data); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	delivered := 0
	var firstErr error
	for _, item := range items {
		e, parseErr := Decode(item.data)
		if parseErr != nil || e.Key() != item.key || e.EventID != item.id {
			err = errors.New("invalid outbox envelope")
		} else {
			call, cancel := context.WithTimeout(ctx, time.Second)
			err = producer.Publish(call, item.key, item.data)
			cancel()
		}
		if err != nil {
			_, _ = db.Exec(ctx, "SELECT rec_stream.retry($1,$2)", item.id, token)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var ack bool
		if err = db.QueryRow(ctx, "SELECT rec_stream.ack($1,$2)", item.id, token).Scan(&ack); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if ack {
			delivered++
		}
	}
	return delivered, firstErr
}
