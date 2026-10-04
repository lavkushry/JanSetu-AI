package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func textValid(value string, min, max int) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(value))
	return n >= min && n <= max
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func (a *App) transaction(ctx context.Context, actor *Actor, fn func(*dbgen.Queries) error) error {
	if err := require(actor); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, a.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		// Serialize local pilot commands before row locks. Replace this coarse lock
		// with ordered aggregate locks before enabling a multi-city deployment.
		if err := q.LockIdempotency(ctx, 77120261004); err != nil {
			return err
		}
		principal, err := q.LockPrincipal(ctx, actor.PrincipalID)
		if err != nil {
			return err
		}
		if principal.State != "ACTIVE" {
			return forbidden()
		}
		profiles, err := q.LockProfiles(ctx, []uuid.UUID{actor.ProfileID})
		if err != nil {
			return err
		}
		if len(profiles) != 1 || profiles[0].State != "ACTIVE" {
			return forbidden()
		}
		roles, err := q.PlatformRoles(ctx, actor.PrincipalID)
		if err != nil {
			return err
		}
		grants, err := q.AgencyGrants(ctx, actor.PrincipalID)
		if err != nil {
			return err
		}
		fresh := *actor
		fresh.Roles = roles
		fresh.Agencies = grants
		// Actor is request-local; subsequent domain checks use fresh grant state.
		*actor = fresh
		return fn(q)
	})
}

func creationKey(r *http.Request) (string, error) {
	s := r.Header.Get("Idempotency-Key")
	if len(s) < 8 || len(s) > 128 {
		return "", invalid("Provide a stable submission key")
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return "", invalid("Invalid submission key")
		}
	}
	return s, nil
}
func (a *App) createCommand(r *http.Request, actor *Actor, operation string, body any, fn func(*dbgen.Queries) (uuid.UUID, any, error)) (json.RawMessage, error) {
	key, err := creationKey(r)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(jsonBytes(body))
	var result []byte
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		lock := sha256.Sum256([]byte(actor.PrincipalID.String() + "|" + operation + "|" + key))
		if err := q.LockIdempotency(r.Context(), int64(binary.BigEndian.Uint64(lock[:8]))); err != nil {
			return err
		}
		receipt, err := q.GetIdempotency(r.Context(), dbgen.GetIdempotencyParams{PrincipalRef: actor.PrincipalID, Operation: operation, IdempotencyKey: key})
		if err == nil {
			if !bytes.Equal(hash[:], receipt.RequestHash) {
				return failure(409, "IDEMPOTENCY_CONFLICT", "This submission key was already used for different content")
			}
			result = receipt.ResponseBody
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		resource, response, err := fn(q)
		if err != nil {
			return err
		}
		result = jsonBytes(response)
		return q.InsertIdempotency(r.Context(), dbgen.InsertIdempotencyParams{PrincipalRef: actor.PrincipalID, Operation: operation, IdempotencyKey: key, RequestHash: hash[:], ResultResourceID: &resource, ResponseCode: 201, ResponseBody: result})
	})
	return result, err
}
func addEvent(ctx context.Context, q *dbgen.Queries, kind string, id uuid.UUID, version int64, event string, payload any) error {
	return q.AddEvent(ctx, dbgen.AddEventParams{ID: uuid.New(), AggregateType: kind, AggregateID: id, AggregateVersion: version, EventType: event, Payload: jsonBytes(payload)})
}
func (a *App) canPost(ctx context.Context, q *dbgen.Queries, actor *Actor, communityID *uuid.UUID) error {
	if communityID == nil {
		return nil
	}
	c, err := q.LockCommunity(ctx, *communityID)
	if err != nil {
		return unavailable()
	}
	if c.State != "ACTIVE" || c.Visibility == "PRIVATE" {
		return forbidden()
	}
	m, err := q.Membership(ctx, dbgen.MembershipParams{CommunityID: *communityID, ProfileID: actor.ProfileID})
	if err != nil || m.State != "ACTIVE" {
		return failure(403, "MEMBERSHIP_REQUIRED", "Join the community before posting")
	}
	if c.Visibility == "RESTRICTED" && m.Role == "MEMBER" {
		return forbidden()
	}
	return nil
}
