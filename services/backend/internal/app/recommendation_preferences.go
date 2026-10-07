package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"golang.org/x/text/language"
)

type recommendationPreference struct {
	PersonalizationEnabled bool     `json:"personalizationEnabled"`
	Interests              []string `json:"interests"`
	Languages              []string `json:"languages"`
	Locality               string   `json:"locality"`
	Generation             int64    `json:"generation"`
	Version                int64    `json:"version"`
}

func readRecommendationPreference(ctx context.Context, db dbgen.DBTX, viewer uuid.UUID) (recommendationPreference, error) {
	p := recommendationPreference{Interests: []string{}, Languages: []string{}, Generation: 1, Version: 1}
	err := db.QueryRow(ctx, `SELECT personalization_enabled,interests,languages,locality,generation,version FROM social.recommendation_preference WHERE profile_id=$1`, viewer).Scan(&p.PersonalizationEnabled, &p.Interests, &p.Languages, &p.Locality, &p.Generation, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return p, err
}

// Recommendation commands serialize only this principal/profile, in the same
// principal -> profile -> aggregate order used by existing account commands.
func (a *App) recommendationTransaction(ctx context.Context, actor *Actor, fn func(pgx.Tx) error) error {
	if err := require(actor); err != nil {
		return err
	}
	tx, err := a.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state *string
	if err = tx.QueryRow(ctx, `SELECT authz.lock_principal($1)`, actor.PrincipalID).Scan(&state); err != nil {
		return err
	}
	if state == nil || *state != "ACTIVE" {
		return forbidden()
	}
	if err = a.checkSession(ctx, tx, actor); err != nil {
		return err
	}
	// NO KEY UPDATE still serializes state/consent changes, while allowing the
	// KEY SHARE lock that notification foreign keys take on this profile. A feed
	// exposure also references a post, so FOR UPDATE here would reverse the
	// notification worker's post -> recipient-profile order and create a cycle.
	if err = tx.QueryRow(ctx, `SELECT state FROM social.profile WHERE id=$1 AND id=authz.current_profile() FOR NO KEY UPDATE`, actor.ProfileID).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return forbidden()
	} else if err != nil {
		return err
	}
	if state == nil || *state != "ACTIVE" {
		return forbidden()
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (a *App) recommendationPreferences(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	p, err := readRecommendationPreference(r.Context(), a.store(r.Context()), actor.ProfileID)
	return p, 200, err
}
func (a *App) saveRecommendationPreferences(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		PersonalizationEnabled bool     `json:"personalizationEnabled"`
		Interests              []string `json:"interests"`
		Languages              []string `json:"languages"`
		Locality               string   `json:"locality"`
	}
	if err = decodeRequired(r, &b, "personalizationEnabled", "interests", "languages", "locality"); err != nil {
		return nil, 0, err
	}
	if len(b.Interests) > 20 || len(b.Languages) > 10 || !textValid(b.Locality, 0, 80) {
		return nil, 0, invalid("Choose up to 20 community interests and 10 languages")
	}
	for i, s := range b.Interests {
		if !textValid(s, 1, 80) || strings.IndexFunc(s, func(c rune) bool { return !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-') }) >= 0 {
			return nil, 0, invalid("Use community slugs for interests")
		}
		b.Interests[i] = strings.ToLower(s)
	}
	for i, s := range b.Languages {
		tag, e := language.Parse(s)
		if e != nil || len(s) > 40 {
			return nil, 0, invalid("Choose valid language tags")
		}
		b.Languages[i] = tag.String()
	}
	slices.Sort(b.Interests)
	b.Interests = slices.Compact(b.Interests)
	slices.Sort(b.Languages)
	b.Languages = slices.Compact(b.Languages)
	var result recommendationPreference
	err = a.recommendationTransaction(r.Context(), actor, func(tx pgx.Tx) error {
		old, e := readRecommendationPreference(r.Context(), tx, actor.ProfileID)
		if e != nil {
			return e
		}
		if old.Version != version {
			return conflict()
		}
		result = old
		if old.PersonalizationEnabled == b.PersonalizationEnabled && reflect.DeepEqual(old.Interests, b.Interests) && reflect.DeepEqual(old.Languages, b.Languages) && old.Locality == b.Locality {
			return nil
		}
		result = recommendationPreference{b.PersonalizationEnabled, b.Interests, b.Languages, b.Locality, old.Generation + 1, old.Version + 1}
		_, e = tx.Exec(r.Context(), `INSERT INTO social.recommendation_preference(profile_id,personalization_enabled,interests,languages,locality,generation,version) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(profile_id) DO UPDATE SET personalization_enabled=$2,interests=$3,languages=$4,locality=$5,generation=$6,version=$7`, actor.ProfileID, result.PersonalizationEnabled, result.Interests, result.Languages, result.Locality, result.Generation, result.Version)
		if e != nil {
			return e
		}
		return clearRecommendationHistory(r.Context(), tx, actor.ProfileID)
	})
	return result, 200, err
}
func clearRecommendationHistory(ctx context.Context, tx pgx.Tx, viewer uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
	return err
}
func (a *App) resetRecommendationHistory(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var result recommendationPreference
	err = a.recommendationTransaction(r.Context(), actor, func(tx pgx.Tx) error {
		p, e := readRecommendationPreference(r.Context(), tx, actor.ProfileID)
		if e != nil {
			return e
		}
		if p.Version != version {
			return conflict()
		}
		_, e = tx.Exec(r.Context(), `INSERT INTO social.recommendation_preference(profile_id,generation,version) VALUES($1,2,2) ON CONFLICT(profile_id) DO UPDATE SET generation=social.recommendation_preference.generation+1,version=social.recommendation_preference.version+1`, actor.ProfileID)
		if e != nil {
			return e
		}
		if e = clearRecommendationHistory(r.Context(), tx, actor.ProfileID); e != nil {
			return e
		}
		result, e = readRecommendationPreference(r.Context(), tx, actor.ProfileID)
		return e
	})
	return result, 200, err
}
func (a *App) recommendationEvent(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	var b struct {
		EventID            uuid.UUID `json:"eventId"`
		ExposureID         uuid.UUID `json:"exposureId"`
		Kind               string    `json:"kind"`
		ActiveMilliseconds int64     `json:"activeMilliseconds"`
	}
	if err := decodeRequired(r, &b, "eventId", "exposureId", "kind"); err != nil {
		return nil, 0, err
	}
	if b.EventID == uuid.Nil || b.ExposureID == uuid.Nil || b.ActiveMilliseconds < 0 || b.ActiveMilliseconds > 600000 {
		return nil, 0, invalid("Invalid recommendation event")
	}
	switch b.Kind {
	case "READ", "SKIP", "MORE", "LESS", "SATISFIED", "DISSATISFIED":
	default:
		return nil, 0, invalid("Unsupported recommendation event")
	}
	if b.Kind != "READ" && b.ActiveMilliseconds != 0 {
		return nil, 0, invalid("Duration applies only to active reading")
	}
	hash := sha256.Sum256(jsonBytes(b))
	err := a.recommendationTransaction(r.Context(), actor, func(tx pgx.Tx) error {
		p, e := readRecommendationPreference(r.Context(), tx, actor.ProfileID)
		if e != nil {
			return e
		}
		if !p.PersonalizationEnabled {
			return forbidden()
		}
		// A retry acknowledges the existing write without recording new behavior.
		// It remains owner/generation scoped even if the exposure has since expired
		// or the public revision is no longer eligible.
		var existing []byte
		e = tx.QueryRow(r.Context(), `SELECT request_hash FROM social.recommendation_event WHERE id=$1 AND profile_id=$2 AND generation=$3`, b.EventID, actor.ProfileID, p.Generation).Scan(&existing)
		if e == nil {
			if string(existing) != string(hash[:]) {
				return failure(409, "EVENT_CONFLICT", "This event or exposure action was already submitted")
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var post uuid.UUID
		var revision int32
		var elapsed int64
		e = tx.QueryRow(r.Context(), `SELECT post_id,revision,GREATEST(0,extract(epoch FROM (statement_timestamp()-created_at))*1000)::bigint FROM social.recommendation_exposure WHERE id=$1 AND profile_id=$2 AND generation=$3 AND expires_at>statement_timestamp()`, b.ExposureID, actor.ProfileID, p.Generation).Scan(&post, &revision, &elapsed)
		if errors.Is(e, pgx.ErrNoRows) {
			return unavailable()
		}
		if e != nil {
			return e
		}
		data, e := dbgen.New(tx).RecommendationPosts(r.Context(), dbgen.RecommendationPostsParams{PostIds: []uuid.UUID{post}, ViewerID: actor.ProfileID})
		if e != nil {
			return e
		}
		if len(data) != 1 {
			return unavailable()
		}
		var current struct {
			PublishedRevision int32
			Body              string
		}
		if e = json.Unmarshal(data[0], &current); e != nil {
			return e
		}
		if current.PublishedRevision != revision {
			return unavailable()
		}
		if b.ActiveMilliseconds > elapsed+1000 {
			return invalid("Reading time exceeds this exposure's age")
		}
		normalized := 0.0
		if b.Kind == "READ" {
			expectedMS := max(3000, len([]rune(current.Body))*300)
			normalized = min(1, float64(b.ActiveMilliseconds)/float64(expectedMS))
		}
		tag, e := tx.Exec(r.Context(), `INSERT INTO social.recommendation_event(id,profile_id,exposure_id,generation,kind,normalized_read,request_hash) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, b.EventID, actor.ProfileID, b.ExposureID, p.Generation, b.Kind, normalized, hash[:])
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var existing []byte
			e = tx.QueryRow(r.Context(), `SELECT request_hash FROM social.recommendation_event WHERE id=$1 AND profile_id=$2`, b.EventID, actor.ProfileID).Scan(&existing)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if e != nil || string(existing) != string(hash[:]) {
				return failure(409, "EVENT_CONFLICT", "This event or exposure action was already submitted")
			}
		}
		return nil
	})
	return map[string]any{"accepted": err == nil}, 200, err
}
