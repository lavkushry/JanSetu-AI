package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type notificationPreferenceResult struct {
	InApp   bool
	Version int64
}

func notificationPreference(t *testing.T, c client) notificationPreferenceResult {
	t.Helper()
	w := c.request("GET", "me/notification-preferences", nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[notificationPreferenceResult](t, w)
}
func muteTarget(t *testing.T, c client, kind string, target uuid.UUID, active bool, expiry *time.Time) {
	t.Helper()
	mustStatus(t, c.request("PUT", "me/mutes", map[string]any{"targetType": kind, "targetId": target, "active": active, "expiresAt": expiry}, 0, ""), 200)
}
func TestNotificationPreferencesVersionOwnershipAndDelivery(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	ownerID := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.feed_preference WHERE profile_id=$1", ownerID)
	})
	baseline := notificationPreference(t, owner)
	if !baseline.InApp || baseline.Version != 1 {
		t.Fatal("missing default notification preference")
	}
	mustStatus(t, client{app: a}.request("GET", "me/notification-preferences", nil, 0, ""), 401)
	mustStatus(t, owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": false}, 0, ""), 428)
	mustStatus(t, owner.request("PATCH", "me/notification-preferences", map[string]any{}, 1, ""), 422)
	w := owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": false}, 1, "")
	mustStatus(t, w, 200)
	saved := parsed[notificationPreferenceResult](t, w)
	if saved.InApp || saved.Version != 2 || unreadActivity(t, owner) != 0 {
		t.Fatal("disabled notifications remained eligible")
	}
	mustStatus(t, owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": true}, 1, ""), 412)
	w = owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": false}, 2, "")
	mustStatus(t, w, 200)
	if parsed[notificationPreferenceResult](t, w).Version != 2 {
		t.Fatal("no-op preference bumped version")
	}
	if !notificationPreference(t, writer).InApp {
		t.Fatal("another account's preference changed")
	}
	ctx := scopedContext(writer, a.DB, vault.Grant{})
	var count int
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.feed_preference WHERE profile_id=$1", ownerID).Scan(&count); e != nil || count != 0 {
		t.Fatal("foreign preference readable", e)
	}
	if tag, e := a.store(ctx).Exec(ctx, "UPDATE social.feed_preference SET version=version+1 WHERE profile_id=$1", ownerID); e != nil || tag.RowsAffected() != 0 {
		t.Fatal("foreign preference writable", e)
	}
	if _, e := a.store(ctx).Exec(ctx, "INSERT INTO social.feed_preference(profile_id,notification_channels,policy_version) VALUES($1,'{}','forged')", ownerID); e == nil {
		t.Fatal("foreign preference insert accepted")
	}
	deniedSQL(t, a.DB, "UPDATE social.feed_preference SET locale='xx' WHERE profile_id=$1", ownerID)
	// A reply approved before worker delivery must respect changed consent.
	p := published(t, a, owner, mod, "Preference delivery fixture")
	approvedReply(t, writer, mod, p.ID, nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("worker ignored notification consent")
	}
	// Preserve other channel choices and unrelated settings on a scoped PATCH.
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.feed_preference SET locale='hi-IN',notification_channels=ARRAY['EMAIL'] WHERE profile_id=$1", ownerID); e != nil {
		t.Fatal(e)
	}
	w = owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": true}, 2, "")
	mustStatus(t, w, 200)
	var preserved bool
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT locale='hi-IN' AND 'EMAIL'=ANY(notification_channels) AND 'IN_APP'=ANY(notification_channels) FROM social.feed_preference WHERE profile_id=$1", ownerID).Scan(&preserved); e != nil || !preserved {
		t.Fatal("other preference fields changed", e)
	}
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("skipped alert was backfilled")
	}
	approvedReply(t, writer, mod, p.ID, nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 1 {
		t.Fatal("enabled notifications did not resume")
	}
	// Two edits using the same version cannot silently overwrite each other.
	mustStatus(t, owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": false}, 3, ""), 200)
	mustStatus(t, owner.request("PATCH", "me/notification-preferences", map[string]any{"inApp": true}, 3, ""), 412)
}

type mutePageResult struct {
	Items []struct {
		ID         uuid.UUID
		TargetID   uuid.UUID
		TargetType string
		Target     any
		Active     bool
		ExpiresAt  *time.Time
		CreatedAt  time.Time
	}
	NextCursor *string
}

func mutePage(t *testing.T, c client, path string) mutePageResult {
	t.Helper()
	w := c.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[mutePageResult](t, w)
}
func TestMutesLiveFeedSnapshotsExplicitAccessAndOwnership(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	ownerID, writerID := myProfileID(t, owner), myProfileID(t, writer)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.mute WHERE profile_id=$1", ownerID)
	})
	marker := "Mute fixture " + uuid.NewString()
	p := published(t, a, writer, mod, marker)
	communityID := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	flag(t, owner, "following", writerID, true)
	t.Cleanup(func() { flag(t, owner, "following", writerID, false) })
	mustStatus(t, owner.request("PUT", "posts/"+p.ID.String()+"/bookmark", map[string]any{"enabled": true}, 0, ""), 200)
	// An old feed cursor deliberately carries the source before it is muted.
	c := feedCursor{Viewer: ownerID, Query: "HOME||" + uuid.Nil.String(), Expires: time.Now().Add(time.Minute).Unix(), Refs: []feedRef{{Type: "POST", ID: p.ID}}}
	path := "feed?cursor=" + a.encodeCursor(c)
	mustStatus(t, owner.request("GET", path, nil, 0, ""), 200)
	muteTarget(t, owner, "PROFILE", writerID, true, nil)
	list := mutePage(t, owner, "me/mutes")
	if len(list.Items) != 1 || !list.Items[0].Active || list.Items[0].Target == nil {
		t.Fatal("mute list lost target")
	}
	first := list.Items[0]
	muteTarget(t, owner, "PROFILE", writerID, true, nil)
	if latest := mutePage(t, owner, "me/mutes").Items[0]; latest.ID != first.ID || !latest.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("mute retry duplicated or reordered the row")
	}
	w := owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatal("old feed snapshot retained muted post")
	}
	w = owner.request("GET", "search?q="+url.QueryEscape(marker), nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatal("muted post stayed in search")
	}
	w = writer.request("GET", "search?q="+url.QueryEscape(marker), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatal("mute affected another reader")
	}
	mustStatus(t, owner.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 200)
	w = owner.request("GET", "me/bookmarks", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatal("explicit bookmark hidden by mute")
	}
	w = owner.request("GET", "profiles/"+writerID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"muted":true`) || !strings.Contains(w.Body.String(), `"following":true`) {
		t.Fatal("mute removed follow or viewer flag")
	}
	w = writer.request("GET", "profiles/"+writerID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"muted":false`) {
		t.Fatal("mute flag disclosed to target")
	}
	ctx := scopedContext(writer, a.DB, vault.Grant{})
	var count int
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.mute WHERE profile_id=$1", ownerID).Scan(&count); e != nil || count != 0 {
		t.Fatal("foreign mute readable", e)
	}
	if tag, e := a.store(ctx).Exec(ctx, "DELETE FROM social.mute WHERE profile_id=$1", ownerID); e != nil || tag.RowsAffected() != 0 {
		t.Fatal("foreign mute removal escaped RLS", e)
	}
	if _, e := a.store(ctx).Exec(ctx, "INSERT INTO social.mute(id,profile_id,muted_profile_id) VALUES($1,$2,$3)", uuid.New(), ownerID, writerID); e == nil {
		t.Fatal("foreign mute insert accepted")
	}
	deniedSQL(t, a.DB, "UPDATE social.mute SET profile_id=$1 WHERE id=$2", writerID, first.ID)
	// Expiry restores discovery; changing the relationship is unnecessary.
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.mute SET expires_at=now()-interval '1 second' WHERE id=$1", first.ID); e != nil {
		t.Fatal(e)
	}
	if mutePage(t, owner, "me/mutes").Items[0].Active {
		t.Fatal("expired mute still active")
	}
	w = owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatal("expired mute did not restore feed source")
	}
	muteTarget(t, owner, "COMMUNITY", communityID, true, nil)
	w = owner.request("GET", "communities/"+communityID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"muted":true`) {
		t.Fatal("community mute flag missing")
	}
	w = owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatal("community mute did not suppress snapshot")
	}
	muteTarget(t, owner, "COMMUNITY", communityID, false, nil)
	muteTarget(t, owner, "PROFILE", writerID, false, nil)
	if len(mutePage(t, owner, "me/mutes").Items) != 0 {
		t.Fatal("mute removals did not persist")
	}
	muteTarget(t, owner, "PROFILE", writerID, true, nil)
	mustStatus(t, writer.request("DELETE", "posts/"+p.ID.String(), nil, p.Version, ""), 204)
	w = owner.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), `"mutedAuthor":true`) || strings.Contains(w.Body.String(), writerID.String()) {
		t.Fatal("deleted post disclosed author through its mute flag")
	}
}

func TestMutesSuppressQueuedRepliesWithoutBackfill(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	ownerID, writerID := myProfileID(t, owner), myProfileID(t, writer)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.mute WHERE profile_id=$1", ownerID)
	})
	p := published(t, a, owner, mod, "Queued mute delivery fixture")
	approvedReply(t, writer, mod, p.ID, nil)
	muteTarget(t, owner, "PROFILE", writerID, true, nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("queued muted reply was delivered")
	}
	muteTarget(t, owner, "PROFILE", writerID, false, nil)
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("skipped muted reply was backfilled")
	}
	approvedReply(t, writer, mod, p.ID, nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 1 {
		t.Fatal("reply delivery did not resume")
	}
	communityID := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	muteTarget(t, owner, "COMMUNITY", communityID, true, nil)
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("community mute kept reply activity")
	}
}

func TestMuteValidationPaginationAndUnavailableTargets(t *testing.T) {
	a := testApp(t)
	owner, writer := login(t, a, 0), login(t, a, 1)
	ownerID, writerID := myProfileID(t, owner), myProfileID(t, writer)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.mute WHERE profile_id=$1", ownerID)
	})
	base := map[string]any{"targetType": "PROFILE", "targetId": writerID, "active": true}
	mustStatus(t, client{app: a}.request("GET", "me/mutes", nil, 0, ""), 401)
	mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "PROFILE", "targetId": writerID}, 0, ""), 422)
	mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "UNKNOWN", "targetId": writerID, "active": true}, 0, ""), 422)
	mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "PROFILE", "targetId": ownerID, "active": true}, 0, ""), 422)
	base["expiresAt"] = time.Now().Add(-time.Minute)
	mustStatus(t, owner.request("PUT", "me/mutes", base, 0, ""), 422)
	base["expiresAt"] = time.Now().Add(366 * 24 * time.Hour)
	mustStatus(t, owner.request("PUT", "me/mutes", base, 0, ""), 422)
	expiry := time.Now().Add(time.Hour)
	muteTarget(t, owner, "PROFILE", writerID, true, &expiry)
	flag(t, writer, "blocks", ownerID, true)
	t.Cleanup(func() { flag(t, writer, "blocks", ownerID, false) })
	if mutePage(t, owner, "me/mutes").Items[0].Target != nil {
		t.Fatal("blocked target metadata leaked in mute list")
	}
	base["expiresAt"] = nil
	mustStatus(t, owner.request("PUT", "me/mutes", base, 0, ""), 404)
	muteTarget(t, owner, "PROFILE", writerID, false, nil)
	flag(t, writer, "blocks", ownerID, false)
	communityID := uuid.New()
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state) VALUES($1,$2,'Muted community fixture','TOPIC','PUBLIC','Fixture rules','ACTIVE')", communityID, "mute_"+strings.ReplaceAll(communityID.String(), "-", "")); e != nil {
		t.Fatal(e)
	}
	for _, state := range []struct{ visibility, state string }{{"PRIVATE", "ACTIVE"}, {"PUBLIC", "ARCHIVED"}} {
		if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.community SET visibility='PUBLIC',state='ACTIVE' WHERE id=$1", communityID); e != nil {
			t.Fatal(e)
		}
		muteTarget(t, owner, "COMMUNITY", communityID, true, nil)
		if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.community SET visibility=$2,state=$3 WHERE id=$1", communityID, state.visibility, state.state); e != nil {
			t.Fatal(e)
		}
		items := mutePage(t, owner, "me/mutes").Items
		if len(items) != 1 || items[0].Target != nil || items[0].TargetID != communityID {
			t.Fatal("unavailable community metadata leaked or owner lost removal target")
		}
		mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "COMMUNITY", "targetId": communityID, "active": true}, 0, ""), 404)
		muteTarget(t, owner, "COMMUNITY", communityID, false, nil)
		if len(mutePage(t, owner, "me/mutes").Items) != 0 {
			t.Fatal("unavailable community mute could not be removed")
		}
	}
	// Bulk fixture is isolated to the temporary database. No account provisioning required.
	for i := 0; i < 25; i++ {
		pid := uuid.New()
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'Muted fixture','DEACTIVATED')", pid, "mute_"+strings.ReplaceAll(pid.String(), "-", "")[:20]); e != nil {
			t.Fatal(e)
		}
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.mute(id,profile_id,muted_profile_id) VALUES($1,$2,$3)", uuid.New(), ownerID, pid); e != nil {
			t.Fatal(e)
		}
	}
	first := mutePage(t, owner, "me/mutes")
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("mute keyset pagination missing")
	}
	for _, item := range first.Items {
		if item.Target != nil {
			t.Fatal("inactive target metadata leaked")
		}
	}
	mustStatus(t, writer.request("GET", "me/mutes?cursor="+*first.NextCursor, nil, 0, ""), 422)
	second := mutePage(t, owner, "me/mutes?cursor="+*first.NextCursor)
	if len(second.Items) != 5 || second.NextCursor != nil {
		t.Fatal("mute pages lost rows")
	}
	c, e := a.profilePageCursor(*first.NextCursor, "mutes", ownerID, ownerID)
	if e != nil {
		t.Fatal(e)
	}
	c.Expires = time.Now().Add(-time.Second).Unix()
	mustStatus(t, owner.request("GET", "me/mutes?cursor="+a.encodeProfileCursor(c), nil, 0, ""), 410)
	muteTarget(t, owner, "PROFILE", first.Items[0].TargetID, false, nil)
	if len(mutePage(t, owner, "me/mutes").Items) != 20 {
		t.Fatal("inactive target mute could not be removed")
	}
}
