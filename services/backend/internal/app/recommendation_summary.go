package app

import (
	"net/http"

	"github.com/jackc/pgx/v5"
)

type recommendationFeedbackSummary struct {
	Generation int64 `json:"generation"`
	More       int64 `json:"more"`
	Less       int64 `json:"less"`
	Helpful    int64 `json:"helpful"`
	NotHelpful int64 `json:"notHelpful"`
}

// Serialize with consent/reset so the counts and generation describe one state.
// The signed viewer scope and owner RLS also constrain the ledger read.
func (a *App) recommendationFeedback(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	result := recommendationFeedbackSummary{}
	err := a.recommendationTransaction(r.Context(), actor, func(tx pgx.Tx) error {
		p, err := readRecommendationPreference(r.Context(), tx, actor.ProfileID)
		if err != nil {
			return err
		}
		result.Generation = p.Generation
		if !p.PersonalizationEnabled {
			return nil
		}
		return tx.QueryRow(r.Context(), `SELECT
    count(*) FILTER (WHERE kind='MORE'), count(*) FILTER (WHERE kind='LESS'),
    count(*) FILTER (WHERE kind='SATISFIED'), count(*) FILTER (WHERE kind='DISSATISFIED')
    FROM social.recommendation_event
    WHERE profile_id=$1 AND generation=$2 AND created_at>statement_timestamp()-interval '30 days'`,
			actor.ProfileID, p.Generation).Scan(&result.More, &result.Less, &result.Helpful, &result.NotHelpful)
	})
	return result, http.StatusOK, err
}
