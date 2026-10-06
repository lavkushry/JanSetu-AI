package app

import (
	"context"
	"encoding/hex"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"net/http"
	"strings"
)

type databaseScopeKey struct{}
type databaseScope struct {
	Session, Trace, Claim, Signature string
	Pool                             *pgxpool.Pool
}

func (a *App) requestScope(r *http.Request, trace string) *http.Request {
	s := &databaseScope{Trace: trace, Pool: a.DB}
	if cookie, err := r.Cookie("jansetu_session"); err == nil && len(cookie.Value) == 43 {
		s.Session = cookie.Value
	}
	if strings.Contains(r.Pattern, "/authority/") || strings.Contains(r.Pattern, "/service-reports") || strings.Contains(r.Pattern, "/my-reports") {
		s.Pool = a.Operations
	}
	if strings.Contains(r.Pattern, "/media/") || strings.Contains(r.Pattern, "/analyses/") {
		s.Pool = a.Media
	}
	if strings.Contains(r.Pattern, "/authority/publication-withdrawal-requests") || strings.HasSuffix(r.Pattern, "/publications") || strings.HasSuffix(r.Pattern, "/publication-withdrawals") {
		s.Pool = a.Publication
	}
	return r.WithContext(context.WithValue(r.Context(), databaseScopeKey{}, s))
}
func scope(ctx context.Context) *databaseScope {
	s, _ := ctx.Value(databaseScopeKey{}).(*databaseScope)
	if s == nil {
		return &databaseScope{}
	}
	return s
}
func (a *App) pool(ctx context.Context) *pgxpool.Pool {
	if p := scope(ctx).Pool; p != nil {
		return p
	}
	return a.DB
}
func (a *App) configureScope(ctx context.Context, tx pgx.Tx) error {
	s := scope(ctx)
	hash := ""
	if s.Session != "" {
		hash = hex.EncodeToString(tokenHash(s.Session))
	}
	_, err := tx.Exec(ctx, `SELECT set_config('jansetu.session_hash',$1,true),set_config('jansetu.auth_mode',$2,true),set_config('jansetu.issuer',$3,true),set_config('jansetu.vault_claim',$4,true),set_config('jansetu.vault_signature',$5,true)`, hash, a.Config.AuthMode, a.Config.OIDCIssuer, s.Claim, s.Signature)
	return err
}
func (a *App) begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := a.pool(ctx).BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	if err = a.configureScope(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func (a *App) store(ctx context.Context) dbgen.DBTX { return scopedDatabase{app: a} }

// Every pooled operation installs scope inside its own transaction. SET LOCAL
// vanishes on commit/rollback; scope can never be inherited by the next request.
type scopedDatabase struct{ app *App }

func (d scopedDatabase) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx, err := d.app.begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return tag, err
	}
	return tag, tx.Commit(ctx)
}
func (d scopedDatabase) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx, err := d.app.begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &scopedRows{Rows: rows, tx: tx, ctx: ctx}, nil
}
func (d scopedDatabase) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tx, err := d.app.begin(ctx)
	if err != nil {
		return scopedRow{err: err}
	}
	return scopedRow{row: tx.QueryRow(ctx, sql, args...), tx: tx, ctx: ctx}
}

type scopedRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
	err error
}

func (r scopedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.tx.Rollback(r.ctx)
	if err := r.row.Scan(dest...); err != nil {
		return err
	}
	return r.tx.Commit(r.ctx)
}

type scopedRows struct {
	pgx.Rows
	tx        pgx.Tx
	ctx       context.Context
	closed    bool
	commitErr error
}

func (r *scopedRows) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	if r.Rows.Err() != nil {
		_ = r.tx.Rollback(r.ctx)
	} else {
		r.commitErr = r.tx.Commit(r.ctx)
	}
}
func (r *scopedRows) Next() bool {
	ok := r.Rows.Next()
	if !ok {
		r.Close()
	}
	return ok
}
func (r *scopedRows) Err() error {
	if err := r.Rows.Err(); err != nil {
		return err
	}
	return r.commitErr
}
