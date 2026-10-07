// Package snapshotcache stores bounded, frozen feed references. It is never a
// permission or consent authority; callers must check live state on every page.
package snapshotcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	Budget     = 20 * time.Millisecond
	MaxAge     = 5 * time.Minute
	maxPayload = 64 * 1024
)

var ErrMiss = errors.New("snapshot cache miss")

// Scope includes the authenticated viewer or anonymous cookie binding in Query.
type Scope struct {
	Token      uuid.UUID
	Query      string
	Generation int64
}

type Record struct {
	Payload json.RawMessage
	Expires time.Time
}

type Store interface {
	Load(context.Context, Scope) (Record, error)
	Save(context.Context, Scope, Record) error
	Close() error
}

type Redis struct{ client *redis.Client }

func New(rawURL string) (*Redis, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid snapshot cache URL")
	}
	// URL query parameters cannot expand the optional dependency's time budget.
	opts.MaxRetries = -1
	opts.DialerRetries = 1
	opts.DialTimeout = Budget
	opts.ReadTimeout = Budget
	opts.WriteTimeout = Budget
	opts.PoolTimeout = Budget
	opts.ContextTimeoutEnabled = true
	opts.PoolSize = 32
	opts.MinIdleConns = 0
	opts.DisableIdentity = true
	return &Redis{client: redis.NewClient(opts)}, nil
}

func (r *Redis) Close() error { return r.client.Close() }

func digest(query string) string {
	h := sha256.Sum256([]byte(query))
	return hex.EncodeToString(h[:])
}

func key(s Scope) string {
	return "jansetu:recommendation:snapshots:v1:" + digest(s.Query) + ":" + strconv.FormatInt(s.Generation, 10) + ":" + s.Token.String()
}

type envelope struct {
	Version     int             `json:"version"`
	Token       uuid.UUID       `json:"token"`
	QueryDigest string          `json:"queryDigest"`
	Generation  int64           `json:"generation"`
	Expires     time.Time       `json:"expires"`
	Payload     json.RawMessage `json:"payload"`
}

func validScope(s Scope) bool { return s.Token != uuid.Nil && s.Query != "" && s.Generation > 0 }
func validRecord(r Record) bool {
	return len(r.Payload) > 0 && len(r.Payload) <= maxPayload && json.Valid(r.Payload) &&
		r.Expires.After(time.Now()) && !r.Expires.After(time.Now().Add(MaxAge))
}

func (r *Redis) Load(ctx context.Context, s Scope) (Record, error) {
	if !validScope(s) {
		return Record{}, ErrMiss
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	data, err := r.client.Get(ctx, key(s)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Record{}, ErrMiss
	}
	if err != nil {
		return Record{}, err
	}
	if len(data) > maxPayload+1024 {
		return Record{}, ErrMiss
	}
	var e envelope
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF || e.Version != 1 ||
		e.Token != s.Token || e.QueryDigest != digest(s.Query) || e.Generation != s.Generation {
		return Record{}, ErrMiss
	}
	record := Record{Payload: e.Payload, Expires: e.Expires}
	if !validRecord(record) {
		return Record{}, ErrMiss
	}
	return record, nil
}

func (r *Redis) Save(ctx context.Context, s Scope, record Record) error {
	if !validScope(s) || !validRecord(record) {
		return errors.New("invalid cached snapshot")
	}
	data, err := json.Marshal(envelope{Version: 1, Token: s.Token, QueryDigest: digest(s.Query), Generation: s.Generation, Expires: record.Expires, Payload: record.Payload})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	// Absolute expiry and NX preserve the original lifetime on cursor replay.
	err = r.client.SetArgs(ctx, key(s), data, redis.SetArgs{Mode: "NX", ExpireAt: record.Expires}).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
