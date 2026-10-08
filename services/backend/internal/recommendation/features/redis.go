package features

import (
	"context"
	_ "embed"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrUnavailable = errors.New("feature projection unavailable")

const Budget = 20 * time.Millisecond

type Scope struct {
	Subject    uuid.UUID
	Generation int64
	Enabled    bool
	AsOf       time.Time
}

type Reader interface {
	Load(context.Context, Scope, []Reference) ([]Observation, error)
	Close() error
}

//go:embed reader.lua
var readLua string

type Redis struct{ client *redis.Client }

func New(rawURL string) (*Redis, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid feature shadow Redis URL")
	}
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

func (r *Redis) Load(ctx context.Context, s Scope, refs []Reference) ([]Observation, error) {
	if !s.Enabled || s.Subject == uuid.Nil || s.Generation < 1 || s.AsOf.IsZero() {
		return nil, ErrUnavailable
	}
	if len(refs) > MaxReferences {
		return nil, errors.New("too many feature references")
	}
	if len(refs) == 0 {
		return []Observation{}, nil
	}
	fields := map[string]bool{}
	args := []any{strconv.FormatInt(s.Generation, 10)}
	for _, ref := range refs {
		if ref.PostID == uuid.Nil || ref.Revision < 1 {
			return nil, errors.New("invalid feature reference")
		}
		for _, action := range Actions {
			field := Field(action, ref)
			if !fields[field] {
				fields[field] = true
				args = append(args, field)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	key := Namespace + ":{viewer:" + s.Subject.String() + "}"
	data, err := r.client.Eval(ctx, readLua, []string{key + ":state", key + ":observations"}, args...).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, ErrUnavailable
	}
	if data[0] != "1" || len(data)%2 != 1 || len(data) > 1+2*len(fields) {
		return nil, errors.New("invalid feature result")
	}
	result := []Observation{}
	seen := map[string]bool{}
	for i := 1; i < len(data); i += 2 {
		field := data[i]
		o, err := Decode([]byte(data[i+1]))
		if err != nil || !fields[field] || seen[field] || field != o.Field() {
			return nil, errors.New("invalid feature result")
		}
		seen[field] = true
		if !o.OccurredAt.After(s.AsOf) && o.OccurredAt.After(s.AsOf.Add(-Retention)) {
			result = append(result, o)
		}
	}
	return result, nil
}
