// Package control reads live shared rollout authority. A lookup failure disables
// ranking; it never grants permission or prevents chronological fallback.
package control

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Database interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type State struct {
	Disabled bool  `json:"disabled"`
	Version  int64 `json:"version"`
}

func Load(ctx context.Context, db Database) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	var s State
	err := db.QueryRow(ctx, "SELECT disabled,version FROM rec_serving.current_control()").Scan(&s.Disabled, &s.Version)
	if err != nil {
		return State{Disabled: true}, err
	}
	if s.Version < 1 {
		return State{Disabled: true}, errors.New("invalid serving control")
	}
	return s, nil
}

// Set returns pgx.ErrNoRows when the expected version no longer matches.
func Set(ctx context.Context, db Database, disabled bool, expectedVersion int64) (State, error) {
	var s State
	err := db.QueryRow(ctx, "SELECT disabled,version FROM rec_serving.set_disabled($1,$2)", disabled, expectedVersion).Scan(&s.Disabled, &s.Version)
	return s, err
}
