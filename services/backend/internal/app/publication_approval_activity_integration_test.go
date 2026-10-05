package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

func publicationApprovalID(t *testing.T, target uuid.UUID, revision int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), `SELECT d.id FROM social.moderation_decision d
	 JOIN social.moderation_case m ON m.id=d.moderation_case_id
	 WHERE (m.post_id=$1 OR m.comment_id=$1) AND m.target_version=$2
	 AND m.reporter_ref IS NULL AND m.reason_code='PUBLICATION_REVIEW' AND d.action='ALLOW'`, target, revision).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPublicationApprovalActivityOwnershipRevisionsAndRetries(t *testing.T) {
	for _, kind := range []string{"POST", "COMMENT"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			owner, other, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
			p := initialPost(t, owner)
			target, postID, commentID := p.ID, p.ID, uuid.Nil
			if kind == "COMMENT" {
				p = published(t, a, other, mod, "Separate conversation author "+uuid.NewString())
				w := owner.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Private approval candidate marker"}, 0, uuid.NewString())
				mustStatus(t, w, 201)
				target = parsed[struct{ ID uuid.UUID }](t, w).ID
				postID, commentID = uuid.Nil, target
			}
			drainActivity(t, a)
			var count int
			if err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM social.notification n
			 JOIN social.moderation_decision d ON d.id=n.moderation_decision_id JOIN social.moderation_case m ON m.id=d.moderation_case_id
			 WHERE (m.post_id=$1 OR m.comment_id=$1) AND n.kind='PUBLICATION_APPROVAL'`, target).Scan(&count); err != nil || count != 0 {
				t.Fatal("pending content generated approval activity", count, err)
			}
			review(t, mod, postID, commentID, 1)
			did := publicationApprovalID(t, target, 1)
			drainActivity(t, a)
			items := activityFor(t, owner, did).Items
			if len(items) != 1 || items[0].Kind != "PUBLICATION_APPROVAL" {
				t.Fatal("missing exact author approval", items)
			}
			for _, c := range []client{other, mod} {
				if len(activityFor(t, c, did).Items) != 0 {
					t.Fatal("approval delivered to another account")
				}
				mustStatus(t, c.request("GET", "me/moderation-decisions/"+did.String(), nil, 0, ""), 404)
			}
			raw := owner.request("GET", "me/activity?filter=MODERATION", nil, 0, "")
			mustStatus(t, raw, 200)
			if !strings.Contains(raw.Body.String(), "A publication approval is available for your content.") || !strings.Contains(raw.Body.String(), `"actor":null`) {
				t.Fatal("approval metadata is not fixed and private")
			}
			var stored, payload string
			if err := integrationAdmin.QueryRow(context.Background(), "SELECT row_to_json(n)::text FROM social.notification n WHERE moderation_decision_id=$1", did).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"Private approval candidate marker", "Constructive synthetic community discussion", target.String(), DemoPrincipals[0].String(), DemoPrincipals[2].String()} {
				if strings.Contains(stored, secret) || strings.Contains(raw.Body.String(), secret) {
					t.Fatal("content, note or identity entered approval metadata", secret)
				}
			}
			if err := integrationAdmin.QueryRow(context.Background(), "SELECT payload::text FROM infra.outbox WHERE aggregate_id=$1 AND event_type='PublicationApprovalRecorded'", did).Scan(&payload); err != nil || payload != "{}" {
				t.Fatal("approval outbox contains private data", payload, err)
			}
			deniedSQL(t, a.Worker, "SELECT reason FROM social.moderation_decision")
			deniedSQL(t, a.Worker, "SELECT * FROM identity.principal")
			deniedSQL(t, a.DB, "SELECT * FROM social.activity_review_source")
			for _, forged := range []struct {
				kind      string
				recipient uuid.UUID
				version   int64
			}{{"MODERATION_DECISION", myProfileID(t, owner), 1}, {"PUBLICATION_APPROVAL", myProfileID(t, other), 1}, {"PUBLICATION_APPROVAL", myProfileID(t, owner), 99}} {
				_, err := a.Worker.Exec(context.Background(), "INSERT INTO social.notification(id,recipient_id,event_id,channel,kind,source_version,moderation_decision_id,state) VALUES($1,$2,$3,'IN_APP',$4,$5,$6,'SENT')", uuid.New(), forged.recipient, uuid.New(), forged.kind, forged.version, did)
				var denial *pgconn.PgError
				if !errors.As(err, &denial) || denial.Code != "42501" {
					t.Fatal("wrong approval kind/recipient/version bypassed row policy", err)
				}
			}
			readPath := "me/activity/" + items[0].ID.String() + "/read"
			unread := unreadActivity(t, owner)
			first := owner.request("PUT", readPath, map[string]any{"read": true}, 0, "")
			mustStatus(t, first, 200)
			retry := owner.request("PUT", readPath, map[string]any{"read": true}, 0, "")
			mustStatus(t, retry, 200)
			if first.Body.String() != retry.Body.String() || unreadActivity(t, owner) != unread-1 {
				t.Fatal("approval read state was not persistent/idempotent")
			}
			if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET delivered_at=NULL WHERE aggregate_id=$1 AND event_type='PublicationApprovalRecorded'", did); err != nil {
				t.Fatal(err)
			}
			if err := addEvent(context.Background(), dbgen.New(integrationAdmin), "MODERATION_DECISION", did, 1, "PublicationApprovalRecorded", map[string]any{}); err != nil {
				t.Fatal(err)
			}
			drainActivity(t, a)
			if got := activityFor(t, owner, did).Items; len(got) != 1 || got[0].ReadAt == nil {
				t.Fatal("redelivery duplicated an approval or reset its read state")
			}
			path := "posts/" + target.String()
			edit := map[string]any{"body": "Private edited approval marker", "submitForReview": true}
			if kind == "COMMENT" {
				path = "comments/" + target.String()
				delete(edit, "submitForReview")
			}
			mustStatus(t, owner.request("PATCH", path, edit, 2, ""), 200)
			drainActivity(t, a)
			if len(activityFor(t, owner, did).Items) != 1 {
				t.Fatal("pending edit changed the previous approval")
			}
			review(t, mod, postID, commentID, 2)
			second := publicationApprovalID(t, target, 2)
			drainActivity(t, a)
			if second == did || len(activityFor(t, owner, second).Items) != 1 || len(activityFor(t, owner, did).Items) != 1 {
				t.Fatal("approved revision did not retain independent exact decisions")
			}
			if kind == "COMMENT" && len(activityFor(t, other, p.ID).Items) != 1 {
				t.Fatal("approval and reply audiences were conflated or edit repeated reply delivery")
			}
			mustStatus(t, owner.request("DELETE", path, nil, 4, ""), 204)
			for _, approval := range []uuid.UUID{did, second} {
				if len(activityFor(t, owner, approval).Items) != 1 {
					t.Fatal("deleted source erased retained approval history")
				}
				mustStatus(t, owner.request("GET", "me/moderation-decisions/"+approval.String(), nil, 0, ""), 200)
			}
		})
	}
}

func TestPublicationApprovalActivityResubmissionInactiveAndHistorical(t *testing.T) {
	a := testApp(t)
	owner, mod := login(t, a, 0), login(t, a, 2)
	profileID := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='ACTIVE' WHERE id=$1", profileID)
	})
	p := initialPost(t, owner)
	rejected := restrictInitial(t, mod, p.ID, uuid.Nil)
	drainActivity(t, a)
	mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), map[string]any{"body": "Corrected approval resubmission", "submitForReview": true}, 2, ""), 200)
	drainActivity(t, a)
	review(t, mod, p.ID, uuid.Nil, 2)
	did := publicationApprovalID(t, p.ID, 2)
	drainActivity(t, a)
	if len(activityFor(t, owner, rejected).Items) != 1 || len(activityFor(t, owner, did).Items) != 1 {
		t.Fatal("corrected resubmission lost rejection history or approval notice")
	}
	p = published(t, a, owner, mod, "Inactive publication approval "+uuid.NewString())
	did = publicationApprovalID(t, p.ID, 1)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='SUSPENDED' WHERE id=$1", profileID); err != nil {
		t.Fatal(err)
	}
	drainActivity(t, a)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='ACTIVE' WHERE id=$1", profileID); err != nil {
		t.Fatal(err)
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, did).Items) != 0 {
		t.Fatal("inactive author's queued approval was backfilled")
	}
	mid, legacy := uuid.New(), uuid.New()
	if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_case(id,post_id,target_version,reason_code,grounds,state) VALUES($1,$2,9,'PUBLICATION_REVIEW','Historical synthetic approval','DECIDED')", mid, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason) VALUES($1,$2,1,'ALLOW','legacy',$3,'Historical private approval')", legacy, mid, DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, legacy).Items) != 0 {
		t.Fatal("historical approval without a new event was backfilled")
	}
}

func TestPublicationApprovalActivityConsentRetentionAndSessionScope(t *testing.T) {
	a := testApp(t)
	owner, mod := login(t, a, 0), login(t, a, 2)
	profileID, modID := myProfileID(t, owner), myProfileID(t, mod)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.feed_preference WHERE profile_id=$1", profileID)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.mute WHERE profile_id=$1 AND muted_profile_id=$2", profileID, modID)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.profile_block WHERE blocker_id=$1 AND blocked_id=$2", profileID, modID)
	})
	setReviewConsent(t, owner, false)
	p := published(t, a, owner, mod, "Paused approval fixture "+uuid.NewString())
	paused := publicationApprovalID(t, p.ID, 1)
	drainActivity(t, a)
	setReviewConsent(t, owner, true)
	drainActivity(t, a)
	if len(activityFor(t, owner, paused).Items) != 0 {
		t.Fatal("paused approval was backfilled")
	}
	mustStatus(t, owner.request("GET", "me/moderation-decisions/"+paused.String(), nil, 0, ""), 200)
	muteTarget(t, owner, "PROFILE", modID, true, nil)
	flag(t, owner, "blocks", modID, true)
	p = published(t, a, owner, mod, "Retained approval fixture "+uuid.NewString())
	did := publicationApprovalID(t, p.ID, 1)
	mustStatus(t, owner.request("DELETE", "posts/"+p.ID.String(), nil, p.Version, ""), 204)
	drainActivity(t, a)
	items := activityFor(t, owner, did).Items
	if len(items) != 1 {
		t.Fatal("deletion or moderator block/mute suppressed own approval")
	}
	readPath := "me/activity/" + items[0].ID.String() + "/read"
	mustStatus(t, owner.request("PUT", readPath, map[string]any{"read": true}, 0, ""), 200)
	setReviewConsent(t, owner, false)
	if len(activityFor(t, owner, did).Items) != 0 || unreadActivity(t, owner) != 0 {
		t.Fatal("paused consent exposed approval inbox/counts")
	}
	mustStatus(t, owner.request("PUT", readPath, map[string]any{"read": false}, 0, ""), 404)
	mustStatus(t, owner.request("GET", "me/moderation-decisions/"+did.String(), nil, 0, ""), 200)
	setReviewConsent(t, owner, true)
	if got := activityFor(t, owner, did).Items; len(got) != 1 || got[0].ReadAt == nil {
		t.Fatal("consent resume lost retained approval/read state")
	}
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(owner.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.activity_review_target WHERE source_id=$1", did).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked session retained approval projection", count, err)
	}
	mustStatus(t, owner.request("GET", "me/moderation-decisions/"+did.String(), nil, 0, ""), 401)
}
