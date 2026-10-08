package stream

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BackfillProgress exposes counts only; private/ineligible post IDs and the
// bounded scan's durable cursor stay inside the restricted database function.
type BackfillProgress struct {
	Scanned, Enqueued           int
	Completed                   bool
	TotalScanned, TotalEnqueued int64
}

// BackfillContent atomically queues a bounded page with its checkpoint. Repeating
// a call after an ambiguous commit continues the pass without requeuing its page.
func BackfillContent(ctx context.Context, db *pgxpool.Pool, batchSize int) (BackfillProgress, error) {
	var progress BackfillProgress
	if batchSize < 1 || batchSize > 100 {
		return progress, errors.New("backfill batch must be 1..100")
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := db.QueryRow(call, "SELECT scanned,enqueued,completed,total_scanned,total_enqueued FROM rec_stream.backfill_content($1)", batchSize).Scan(&progress.Scanned, &progress.Enqueued, &progress.Completed, &progress.TotalScanned, &progress.TotalEnqueued)
	return progress, err
}
