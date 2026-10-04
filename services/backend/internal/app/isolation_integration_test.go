package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func deniedSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err == nil {
		t.Fatalf("restricted login allowed %s", sql)
	}
}
func TestRuntimeRolesDenyCrossBoundaryAccess(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	for name, pool := range map[string]*pgxpool.Pool{"auth": a.Auth, "social": a.DB, "operations": a.Operations, "publication": a.Publication, "worker": a.Worker, "vault": integrationVaultRuntime, "vault-auth": integrationVaultAuth} {
		var elevated bool
		if err := pool.QueryRow(ctx, "SELECT rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole OR rolreplication FROM pg_roles WHERE rolname=current_user").Scan(&elevated); err != nil || elevated {
			t.Fatal(name, "is privileged", err)
		}
		deniedSQL(t, pool, "CREATE TABLE public.runtime_escape(id int)")
		deniedSQL(t, pool, "SET ROLE jansetu")
	}
	for _, pool := range []*pgxpool.Pool{a.DB, a.Operations, a.Publication, a.Worker} {
		deniedSQL(t, pool, "SELECT token_hash FROM identity.session")
		deniedSQL(t, pool, "UPDATE identity.platform_grant SET revoked_at=NULL")
		deniedSQL(t, pool, "ALTER TABLE ops.report DISABLE ROW LEVEL SECURITY")
		deniedSQL(t, pool, "INSERT INTO social.case_receipt(id) VALUES($1)", uuid.New())
	}
	deniedSQL(t, a.Worker, "UPDATE social.post SET id=$1 WHERE id=$2", uuid.New(), uuid.MustParse("60000000-0000-4000-8000-000000000001"))
	deniedSQL(t, a.Worker, "SELECT body FROM social.comment")
	deniedSQL(t, a.DB, "SELECT statement FROM ops.report")
	deniedSQL(t, a.Worker, "SELECT statement FROM ops.report")
	deniedSQL(t, a.Publication, "SELECT statement FROM ops.report")
	deniedSQL(t, a.Operations, "SELECT signing_key FROM authz.vault_key")
	deniedSQL(t, integrationVaultAuth, "SELECT token_hash FROM identity.session")
	deniedSQL(t, integrationVaultRuntime, "SELECT principal_ref FROM vault.principal_subject")
	deniedSQL(t, integrationVaultRuntime, "SELECT * FROM vault.access_grant")
	deniedSQL(t, integrationVaultRuntime, "DELETE FROM vault.audit_event")
	deniedSQL(t, integrationVaultRuntime, "UPDATE vault.audit_event SET action='changed'")
	var forced int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ('ops','social','infra') AND c.relrowsecurity AND c.relforcerowsecurity").Scan(&forced); err != nil || forced < 14 {
		t.Fatal("missing forced policies", forced, err)
	}
	for _, role := range []string{"js_auth", "js_social", "js_ops", "js_publication", "js_worker", "js_vault_auth"} {
		conn, err := pgx.Connect(ctx, roleURL(integrationVaultAdmin.Config().ConnString(), role))
		if err == nil {
			conn.Close(ctx)
			t.Fatal(role, "connected to the vault")
		}
	}
	conn, err := pgx.Connect(ctx, roleURL(integrationAdmin.Config().ConnString(), "js_vault"))
	if err == nil {
		conn.Close(ctx)
		t.Fatal("vault role connected to application database")
	}
	pool, err := platform.RuntimePool(ctx, integrationAdmin.Config().ConnString(), "js_social")
	if err == nil {
		pool.Close()
		t.Fatal("administrator accepted as runtime login")
	}
}
func privateFixture(t *testing.T, c client) uuid.UUID {
	t.Helper()
	w := c.request("POST", "service-reports", ReportInput{ClientSubmissionID: uuid.New(), Statement: "Private synthetic boundary test " + uuid.NewString(), LocationLabel: "Fictional junction", Category: "FOOTPATH", PublicationPreference: "PRIVATE"}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	return parsed[struct{ ID uuid.UUID }](t, w).ID
}
func reportCount(t *testing.T, a *App, ctx context.Context, rid uuid.UUID) int {
	t.Helper()
	var count int
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.report WHERE id=$1", rid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func scopedContext(c client, pool *pgxpool.Pool, g vault.Grant) context.Context {
	return context.WithValue(context.Background(), databaseScopeKey{}, &databaseScope{Session: c.cookie.Value, Pool: pool, Claim: g.Claim, Signature: g.Signature})
}
func TestRLSRejectsForgedOwnerAndClearsPooledScope(t *testing.T) {
	a := testApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	own, foreign := privateFixture(t, owner), privateFixture(t, other)
	grant, err := a.Vault.Aliases(context.Background(), owner.cookie.Value, uuid.NewString(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := scopedContext(owner, a.Operations, grant)
	if reportCount(t, a, ctx, own) != 1 || reportCount(t, a, ctx, foreign) != 0 {
		t.Fatal("owner row isolation failed")
	}
	if reportCount(t, a, scopedContext(other, a.Operations, grant), own) != 0 {
		t.Fatal("signed grant transferred to another session")
	}
	var alias uuid.UUID
	if err = integrationAdmin.QueryRow(context.Background(), "SELECT reporter_ref FROM ops.report WHERE id=$1", foreign).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal([]byte(grant.Claim), &payload); err != nil {
		t.Fatal(err)
	}
	payload["aliases"] = []uuid.UUID{alias}
	b, _ := json.Marshal(payload)
	forged := grant
	forged.Claim = string(b)
	if reportCount(t, a, scopedContext(owner, a.Operations, forged), foreign) != 0 {
		t.Fatal("tampered owner list accepted")
	}
	payload["expiresAt"] = time.Now().Add(-time.Minute).Unix()
	b, _ = json.Marshal(payload)
	expired := grant
	expired.Claim = string(b)
	m := hmac.New(sha256.New, integrationKeys.SigningKey)
	m.Write(b)
	expired.Signature = hex.EncodeToString(m.Sum(nil))
	if reportCount(t, a, scopedContext(owner, a.Operations, expired), foreign) != 0 {
		t.Fatal("expired signature accepted")
	}
	// Reuse exactly one acquired connection after both commit and rollback.
	conn, err := a.Operations.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, commit := range []bool{true, false} {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.configureScope(ctx, tx); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM ops.report WHERE id=$1", own).Scan(&count); err != nil || count != 1 {
			t.Fatal("scope not installed", count, err)
		}
		if commit {
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = conn.QueryRow(context.Background(), "SELECT count(*) FROM ops.report").Scan(&count); err != nil || count != 0 {
			t.Fatal("scope leaked across transactions", count, err)
		}
	}
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(owner.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if reportCount(t, a, ctx, own) != 0 {
		t.Fatal("revoked session retained signed grant access")
	}
	if _, err = a.Vault.Aliases(context.Background(), owner.cookie.Value, uuid.NewString(), uuid.Nil); err != vault.ErrUnauthenticated {
		t.Fatal("vault accepted revoked session", err)
	}
}
func TestRLSAgencyAndGrantRevocation(t *testing.T) {
	a := testApp(t)
	officer := login(t, a, 3)
	ctx := scopedContext(officer, a.Operations, vault.Grant{})
	cid, oid := uuid.New(), uuid.New()
	water := uuid.MustParse("30000000-0000-4000-8000-000000000002")
	_, err := integrationAdmin.Exec(context.Background(), "INSERT INTO ops.case_record(id,category_code,state,first_valid_report_at,urgency_tier) VALUES($1,'WATER','OPEN',now(),1)", cid)
	if err != nil {
		t.Fatal(err)
	}
	_, err = integrationAdmin.Exec(context.Background(), "INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state,authority_basis_ref) VALUES($1,$2,$3,'RESTORATION','PROPOSED','synthetic')", oid, cid, water)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.case_record WHERE id=$1", cid).Scan(&count); err != nil || count != 0 {
		t.Fatal("foreign agency case leaked", count, err)
	}
	tag, err := a.store(ctx).Exec(ctx, "UPDATE ops.obligation SET state='ACCEPTED' WHERE id=$1", oid)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatal("foreign task changed", err)
	}
	cityCase := uuid.MustParse("70000000-0000-4000-8000-000000000001")
	if err = a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.case_record WHERE id=$1", cityCase).Scan(&count); err != nil || count != 1 {
		t.Fatal("assigned case inaccessible", count, err)
	}
	_, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.organization_grant SET revoked_at=now() WHERE principal_id=$1", DemoPrincipals[3])
	if err != nil {
		t.Fatal(err)
	}
	defer integrationAdmin.Exec(context.Background(), "UPDATE identity.organization_grant SET revoked_at=NULL WHERE principal_id=$1", DemoPrincipals[3])
	if err = a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.case_record WHERE id=$1", cityCase).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked agency grant retained access", count, err)
	}
}
func TestVaultSelfOnlyContractAndAudit(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	rid := privateFixture(t, owner)
	server := httptest.NewServer((&vault.Service{DB: integrationVaultRuntime, Auth: integrationVaultAuth, Keys: integrationKeys, Token: a.Config.VaultToken, Mode: a.Config.AuthMode, Issuer: a.Config.OIDCIssuer}).Handler())
	defer server.Close()
	request := func(method, path, body, session, token string) *http.Response {
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-JanSetu-Session", session)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if request("GET", "/aliases", "", owner.cookie.Value, "wrong").StatusCode != 401 {
		t.Fatal("missing service authentication accepted")
	}
	if request("GET", "/aliases?principalId="+DemoPrincipals[1].String(), "", owner.cookie.Value, a.Config.VaultToken).StatusCode != 400 {
		t.Fatal("principal selector accepted")
	}
	if request("POST", "/aliases", `{"submissionId":"`+uuid.NewString()+`","principalId":"`+DemoPrincipals[1].String()+`"}`, owner.cookie.Value, a.Config.VaultToken).StatusCode != 400 {
		t.Fatal("foreign owner field accepted")
	}
	if request("GET", "/aliases", "", strings.Repeat("x", 43), a.Config.VaultToken).StatusCode != 401 {
		t.Fatal("invalid resident session accepted")
	}
	r := request("GET", "/aliases", "", owner.cookie.Value, a.Config.VaultToken)
	if r.StatusCode != 200 {
		t.Fatal("self alias read failed")
	}
	var value json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{DemoPrincipals[0].String(), ownerProfile().String(), rid.String(), "principal_ciphertext", "subject_id", "Private synthetic"} {
		if bytes.Contains(value, []byte(secret)) {
			t.Fatal("vault response leaked", secret)
		}
	}
	var legacy int
	if err := integrationVaultAdmin.QueryRow(context.Background(), "SELECT count(*) FROM vault.principal_subject").Scan(&legacy); err != nil || legacy != 0 {
		t.Fatal("plaintext locator retained", legacy, err)
	}
	var allowed, denied int
	err := integrationVaultAdmin.QueryRow(context.Background(), "SELECT count(*) FILTER(WHERE result_code='ALLOWED'),count(*) FILTER(WHERE result_code='DENIED') FROM vault.audit_event").Scan(&allowed, &denied)
	if err != nil || allowed == 0 || denied == 0 {
		t.Fatal("purpose audit missing", err)
	}
}
func TestLegacyLocatorMigrationPreservesAliases(t *testing.T) {
	testApp(t)
	ctx := context.Background()
	principal, subject, alias, submission := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	conn, err := pgx.Connect(ctx, integrationVaultAdmin.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "INSERT INTO vault.subject(id,safe_contact_policy,retention_policy_id) VALUES($1,'{}','test');", subject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, "INSERT INTO vault.principal_subject(principal_ref,subject_id) VALUES($1,$2)", principal, subject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, "INSERT INTO vault.pseudonym_binding(alias_id,subject_id,scope_kind,scope_id) VALUES($1,$2,'REPORT',$3)", alias, subject, submission)
	if err != nil {
		t.Fatal(err)
	}
	if err = vault.UpgradeLegacy(ctx, conn, integrationKeys); err != nil {
		t.Fatal(err)
	}
	if err = vault.UpgradeLegacy(ctx, conn, integrationKeys); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM vault.pseudonym_binding b JOIN vault.account_locator l ON l.subject_id=b.subject_id WHERE b.alias_id=$1 AND b.scope_id=$2", alias, submission).Scan(&count); err != nil || count != 1 {
		t.Fatal("alias or subject replaced", count, err)
	}
	changed := integrationKeys
	changed.Version = "other-version"
	if err = vault.UpgradeLegacy(ctx, conn, changed); err == nil {
		t.Fatal("key version silently changed")
	}
}

func TestVaultKeyBindingFailsClosed(t *testing.T) {
	testApp(t)
	ctx := context.Background()
	if err := vault.VerifyKeys(ctx, integrationVaultRuntime, integrationKeys); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"encryption", "lookup", "signing"} {
		k := integrationKeys
		k.EncryptionKey = append([]byte(nil), k.EncryptionKey...)
		k.LookupKey = append([]byte(nil), k.LookupKey...)
		k.SigningKey = append([]byte(nil), k.SigningKey...)
		switch field {
		case "encryption":
			k.EncryptionKey[0] ^= 1
		case "lookup":
			k.LookupKey[0] ^= 1
		case "signing":
			k.SigningKey[0] ^= 1
		}
		if vault.VerifyKeys(ctx, integrationVaultRuntime, k) == nil {
			t.Fatal("changed " + field + " key accepted")
		}
	}
}

func TestForeignSubmissionIdentifierDeniedWithoutRetry(t *testing.T) {
	a := testApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	input := ReportInput{ClientSubmissionID: uuid.New(), Statement: "A fictional report with a stable client identifier", LocationLabel: "Fictional crossing", Category: "FOOTPATH", PublicationPreference: "PRIVATE"}
	first := owner.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, first, 201)
	mustStatus(t, other.request("POST", "service-reports", input, 0, uuid.NewString()), 404)
	repeat := owner.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, repeat, 201)
	if parsed[struct{ ID uuid.UUID }](t, first).ID != parsed[struct{ ID uuid.UUID }](t, repeat).ID {
		t.Fatal("retry replaced report")
	}
}

func TestSessionExpiryInsideOpenTransaction(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	rid := privateFixture(t, owner)
	grant, err := a.Vault.Aliases(context.Background(), owner.cookie.Value, uuid.NewString(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := scopedContext(owner, a.Operations, grant)
	tx, err := a.begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET expires_at=clock_timestamp()+interval '1 second' WHERE token_hash=$1", tokenHash(owner.cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM ops.report WHERE id=$1", rid).Scan(&count); err != nil || count != 1 {
		t.Fatal("initial scope unavailable", count, err)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_sleep(1.2)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM ops.report WHERE id=$1", rid).Scan(&count); err != nil || count != 0 {
		t.Fatal("transaction retained expired session access", count, err)
	}
}
