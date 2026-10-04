package platform

import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed security/*.sql
var securitySQL embed.FS

func BootstrapRoles(ctx context.Context, conn *pgx.Conn) error {
	b, err := securitySQL.ReadFile("security/roles.sql")
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, string(b))
	return err
}
func InstallPrivileges(ctx context.Context, conn *pgx.Conn, kind string, signingKey []byte) error {
	b, err := securitySQL.ReadFile("security/" + kind + ".sql")
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, conn, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, string(b)); err != nil {
			return err
		}
		var database string
		if err := tx.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
			return err
		}
		// No PUBLIC CONNECT/TEMP and no cross-database runtime access.
		if _, err := tx.Exec(ctx, "REVOKE ALL ON DATABASE "+pgx.Identifier{database}.Sanitize()+" FROM PUBLIC,js_auth,js_social,js_ops,js_publication,js_worker,js_vault,js_vault_auth,js_media,js_media_worker"); err != nil {
			return err
		}
		roles := "js_auth,js_social,js_ops,js_publication,js_worker,js_vault_auth,js_media,js_media_worker"
		if kind == "vault" {
			roles = "js_vault"
		}
		if _, err := tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{database}.Sanitize()+" TO "+roles); err != nil {
			return err
		}
		if kind == "app" {
			if len(signingKey) != 32 {
				return fmt.Errorf("vault signing key must be 32 bytes")
			}
			var existing []byte
			err := tx.QueryRow(ctx, "SELECT signing_key FROM authz.vault_key WHERE singleton=true").Scan(&existing)
			if err == pgx.ErrNoRows {
				_, err = tx.Exec(ctx, "INSERT INTO authz.vault_key(singleton,signing_key) VALUES(true,$1)", signingKey)
			} else if err == nil && string(existing) != string(signingKey) {
				return fmt.Errorf("vault signing key changed; use a reviewed rotation procedure")
			}
			return err
		}
		return nil
	})
}

// RuntimePool rejects accidentally supplied owners, elevated logins, or another service's role.
func RuntimePool(ctx context.Context, dsn, role string) (*pgxpool.Pool, error) {
	p, err := Pool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	var valid bool
	err = p.QueryRow(ctx, `SELECT current_user=$1 AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication
 AND NOT EXISTS(SELECT FROM pg_database WHERE datdba=pg_roles.oid)
 AND NOT EXISTS(SELECT FROM pg_namespace WHERE nspowner=pg_roles.oid)
 AND NOT EXISTS(SELECT FROM pg_auth_members m WHERE m.member=pg_roles.oid)
 AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relowner=pg_roles.oid AND n.nspname NOT IN ('pg_catalog','information_schema'))
 FROM pg_roles WHERE rolname=current_user`, role).Scan(&valid)
	if err != nil || !valid {
		p.Close()
		return nil, fmt.Errorf("runtime requires non-owner restricted role %s", role)
	}
	return p, nil
}
