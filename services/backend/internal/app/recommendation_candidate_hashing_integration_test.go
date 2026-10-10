package app

import (
	"context"
	"crypto/md5"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

// Frozen query from main 6eacea2: the comparison keeps source selection and
// feature semantics observable independently of the optimized query.
//
//go:embed testdata/recommendation_candidates_before_hashing.sql
var recommendationCandidatesBeforeHashing string

func TestRecommendationCandidateHashingAfterSelection(t *testing.T) {
	a := testApp(t)
	viewer := login(t, a, 0)
	profile := myProfileID(t, viewer)
	ctx := context.Background()
	authors := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	community := uuid.New()
	slug := "hashing-" + community.String()
	ids := make([]uuid.UUID, 3500)
	for i := range ids {
		ids[i] = uuid.New()
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := integrationAdmin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
			for _, deletion := range []struct {
				sql string
				arg any
			}{
				{`DELETE FROM social.profile_follow WHERE followed_id=ANY($1)`, authors},
				{`DELETE FROM social.post_revision WHERE post_id=ANY($1)`, ids},
				{`DELETE FROM social.post WHERE id=ANY($1)`, ids},
				{`DELETE FROM social.community WHERE id=$1`, community},
				{`DELETE FROM social.profile WHERE id=ANY($1)`, authors},
			} {
				if _, err := tx.Exec(ctx, deletion.sql, deletion.arg); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
		// Deferred delete triggers run at commit. Remove only this fixture's
		// content envelopes afterward, leaving later stream proofs bounded.
		exec(`DELETE FROM rec_stream.outbox WHERE event_type='CONTENT'
 AND payload->>'postId' IN (SELECT id::text FROM unnest($1::uuid[]) AS id)`, ids)
	})
	for _, author := range authors {
		exec(`INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'Hashing fixture','ACTIVE')`, author, "hash_"+author.String()[:12])
	}
	exec(`INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state)
 VALUES($1,$2,'Hashing fixture','TOPIC','PUBLIC','Fixture rules','ACTIVE')`, community, slug)
	exec(`INSERT INTO social.profile_follow(follower_id,followed_id) VALUES($1,$2)`, profile, authors[1])
	// Disjoint sources: newest neutral posts, older followed posts and oldest
	// interest posts. All fixture dates exceed seed dates, with no age ties.
	err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO social.post(id,author_id,community_id,kind,state,published_revision,published_at)
 SELECT x.id,CASE WHEN n<=1500 THEN $2::uuid WHEN n<=2500 THEN $3::uuid ELSE $4::uuid END,
 CASE WHEN n>2500 THEN $5::uuid ELSE NULL END,'SHORT','PUBLISHED',1,
 transaction_timestamp()+interval '1 hour'-n*interval '1 second'
 FROM unnest($1::uuid[]) WITH ORDINALITY x(id,n)`, ids, authors[0], authors[1], authors[2], community); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state)
 SELECT x.id,1,CASE WHEN n<=2 THEN 'Same published body' ELSE 'Published fixture '||n::text END,
 CASE WHEN n%3=0 THEN 'hi-IN' ELSE 'en-IN' END,'APPROVED'
 FROM unnest($1::uuid[]) WITH ORDINALITY x(id,n)`, ids)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A different current draft must not alter the published-content hash.
	exec(`INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,2,'Private current draft','en-IN','PENDING')`, ids[0])
	exec(`UPDATE social.post SET current_revision=2 WHERE id=$1`, ids[0])
	scope := scopedContext(viewer, a.DB, vault.Grant{})
	for _, languages := range [][]string{{}, {"en-IN"}, {"hi-IN"}} {
		query := "WITH baseline AS (" + recommendationCandidatesBeforeHashing + "), bounded AS (" + recommendationCandidates + `)
 SELECT (SELECT jsonb_agg(b) FROM baseline b),(SELECT jsonb_agg(b) FROM bounded b)`
		var before, after []byte
		if err := a.store(scope).QueryRow(scope, query, profile, []string{slug}, "", uuid.Nil, languages, int64(1), false).Scan(&before, &after); err != nil {
			t.Fatal(err)
		}
		var baseline, bounded []map[string]any
		if json.Unmarshal(before, &baseline) != nil || json.Unmarshal(after, &bounded) != nil || !reflect.DeepEqual(baseline, bounded) {
			t.Fatal("candidate order, identities or features changed", languages)
		}
		if len(bounded) == 0 || len(bounded) > 2000 || (len(languages) == 0 && len(bounded) != 2000) {
			t.Fatal("source selection did not exercise the candidate budget", len(bounded))
		}
		if len(languages) == 0 {
			want := fmt.Sprintf("body:%x", md5.Sum([]byte("Same published body")))
			if bounded[0]["dedup_key"] != want || bounded[1]["dedup_key"] != want || bounded[0]["published_revision"] != float64(1) {
				t.Fatal("deduplication used a draft or lost repeated published bodies")
			}
		}
	}

	// A disposable invoker function counts evaluation without changing runtime
	// code or exposing content. Grants apply only to this isolated test schema.
	schema := pgx.Identifier{"hash_count_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	t.Cleanup(func() { exec("DROP SCHEMA " + schema + " CASCADE") })
	exec("CREATE SCHEMA " + schema)
	exec("CREATE SEQUENCE " + schema + ".calls")
	exec("CREATE FUNCTION " + schema + `.counted_md5(input text) RETURNS text LANGUAGE plpgsql VOLATILE AS $body$
 BEGIN PERFORM nextval('` + schema + `.calls'); RETURN md5(input); END $body$`)
	exec("GRANT USAGE ON SCHEMA " + schema + " TO js_social")
	exec("GRANT USAGE ON SEQUENCE " + schema + ".calls TO js_social")
	exec("GRANT EXECUTE ON FUNCTION " + schema + ".counted_md5(text) TO js_social")
	counts := []int64{}
	for _, query := range []string{recommendationCandidatesBeforeHashing, recommendationCandidates} {
		exec("ALTER SEQUENCE " + schema + ".calls RESTART WITH 1")
		query = strings.ReplaceAll(query, "md5(r.body)", schema+".counted_md5(r.body)")
		query = strings.ReplaceAll(query, "md5(e.published_body)", schema+".counted_md5(e.published_body)")
		rows, err := a.store(scope).Query(scope, query, profile, []string{slug}, "", uuid.Nil, []string{}, int64(1), false)
		if err != nil {
			t.Fatal(err)
		}
		returned := 0
		for rows.Next() {
			returned++
		}
		rows.Close()
		if err := rows.Err(); err != nil || returned != 2000 {
			t.Fatal("instrumented candidate selection failed", returned, err)
		}
		var count int64
		if err := integrationAdmin.QueryRow(ctx, "SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM "+schema+".calls").Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, count)
	}
	if counts[0] < int64(len(ids)) || counts[1] != 2000 {
		t.Fatal("hash work was not bounded after selection", counts)
	}
	t.Logf("hash evaluations: baseline=%d selected=%d; 2000 candidates in each", counts[0], counts[1])
}
