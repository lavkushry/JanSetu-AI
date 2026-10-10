package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

func TestRecommendationRetrievalAuthorEligibility(t *testing.T) {
	a := testApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	viewer := myProfileID(t, owner)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := integrationAdmin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	communities := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	t.Cleanup(func() { exec(`DELETE FROM social.community WHERE id=ANY($1)`, communities) })
	ids := recommendationFixture(t, a)
	authors := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		if err := integrationAdmin.QueryRow(ctx, `SELECT author_id FROM social.post WHERE id=$1`, id).Scan(&authors[i]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		exec(`DELETE FROM social.profile_follow WHERE follower_id=$1 AND followed_id=ANY($2)`, viewer, authors)
		exec(`DELETE FROM social.profile_block WHERE blocker_id=ANY($1) OR blocked_id=ANY($1)`, authors)
		exec(`DELETE FROM social.community_follow WHERE community_id=ANY($1)`, communities)
		exec(`DELETE FROM social.community_member WHERE community_id=ANY($1)`, communities)
	})
	for i, visibility := range []string{"PRIVATE", "PUBLIC", "RESTRICTED", "PUBLIC"} {
		exec(`INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state)
 VALUES($1,$2,'Author eligibility fixture','TOPIC',$3,'Fixture rules','ACTIVE')`, communities[i], "authors-"+communities[i].String(), visibility)
	}
	exec(`UPDATE social.community SET state='FROZEN' WHERE id=$1`, communities[1])
	exec(`UPDATE social.profile SET state='SUSPENDED' WHERE id=$1`, authors[4])
	exec(`UPDATE social.profile SET state='DEACTIVATED' WHERE id=$1`, authors[6])
	// Authorless civic updates remain outside the social candidate sources.
	exec(`UPDATE social.post SET kind='CASE_UPDATE',author_id=NULL,
 receipt_id=(SELECT id FROM social.case_receipt WHERE publication_state='PUBLISHED' LIMIT 1) WHERE id=$1`, ids[2])
	exec(`UPDATE social.post SET state='HIDDEN' WHERE id=$1`, ids[10])
	exec(`UPDATE social.post_revision SET review_state='REJECTED' WHERE post_id=$1`, ids[12])
	for i, post := range []uuid.UUID{ids[14], ids[16], ids[18], ids[28]} {
		exec(`UPDATE social.post SET community_id=$1 WHERE id=$2`, communities[i], post)
	}
	exec(`INSERT INTO social.profile_block(blocker_id,blocked_id) VALUES($1,$2),($3,$1)`, viewer, authors[20], authors[22])
	exec(`UPDATE social.post SET kind='QUOTE',source_post_id=$1 WHERE id=$2`, ids[4], ids[24])
	exec(`UPDATE social.post SET kind='QUOTE',source_post_id=$1 WHERE id=$2`, ids[0], ids[26])
	exec(`INSERT INTO social.profile_follow(follower_id,followed_id) VALUES($1,$2)`, viewer, authors[0])
	exec(`INSERT INTO social.community_follow(profile_id,community_id) VALUES($1,$2)`, viewer, communities[2])
	exec(`INSERT INTO social.community_member(community_id,profile_id,role,state) VALUES($1,$2,'MEMBER','ACTIVE')`, communities[3], viewer)

	read := func(c client, community uuid.UUID, languages []string, personal bool) map[uuid.UUID]map[string]any {
		t.Helper()
		scope := context.WithValue(ctx, databaseScopeKey{}, &databaseScope{Pool: a.DB})
		if c.cookie != nil {
			scope = scopedContext(c, a.DB, vault.Grant{})
		}
		// Compare all ordered references and features at one visibility snapshot
		// and statement timestamp against the frozen pre-optimization query.
		query := "WITH baseline AS (" + recommendationCandidatesBeforeHashing + "), current AS (" + recommendationCandidates + `)
 SELECT coalesce((SELECT jsonb_agg(b) FROM baseline b),'[]'::jsonb),coalesce((SELECT jsonb_agg(c) FROM current c),'[]'::jsonb)`
		var before, after []byte
		if err := a.store(scope).QueryRow(scope, query, viewer, []string{"authors-" + communities[2].String()}, "", community, languages, int64(1), personal).Scan(&before, &after); err != nil {
			t.Fatal(err)
		}
		var baseline, current []map[string]any
		if json.Unmarshal(before, &baseline) != nil || json.Unmarshal(after, &current) != nil || !reflect.DeepEqual(baseline, current) {
			t.Fatal("author eligibility changed ordered candidates or features")
		}
		result := map[uuid.UUID]map[string]any{}
		for _, item := range current {
			result[uuid.MustParse(item["id"].(string))] = item
		}
		return result
	}
	excluded := map[int]bool{2: true, 4: true, 5: true, 6: true, 7: true, 10: true, 12: true, 14: true, 16: true, 20: true, 21: true, 22: true, 23: true, 24: true}
	for _, c := range []client{owner, other, {app: a}} {
		for _, personal := range []bool{false, true} {
			result := read(c, uuid.Nil, []string{}, personal)
			for i, id := range ids {
				_, present := result[id]
				if present == excluded[i] {
					t.Fatalf("fixture %d eligibility: present=%v", i, present)
				}
			}
			for _, i := range []int{0, 1, 18, 28} {
				if result[ids[i]]["relationship"] != float64(1) {
					t.Fatalf("fixture %d explicit relationship changed", i)
				}
			}
		}
	}
	read(owner, communities[2], []string{"en-IN"}, false)
	read(owner, uuid.Nil, []string{"hi-IN"}, false)
	// Membership is evaluated anew each statement; there is no author cache.
	exec(`UPDATE social.profile SET state='ACTIVE' WHERE id=ANY($1)`, []uuid.UUID{authors[4], authors[6]})
	result := read(owner, uuid.Nil, []string{}, false)
	for _, i := range []int{4, 5, 6, 7, 24} {
		if _, present := result[ids[i]]; !present {
			t.Fatalf("reactivated author or repost source %d remained excluded", i)
		}
	}
}
