package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/snapshotcache"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

type recommendationRef struct {
	ID          uuid.UUID `json:"id"`
	Revision    int32     `json:"revision"`
	Explanation string    `json:"explanation"`
	Exposure    uuid.UUID `json:"exposure"`
}
type recommendationSnapshot struct {
	ID            uuid.UUID           `json:"snapshotId"`
	Posts         []recommendationRef `json:"posts"`
	Receipts      []uuid.UUID         `json:"receipts"`
	PostOffset    int                 `json:"postOffset"`
	ReceiptOffset int                 `json:"receiptOffset"`
	Model         string              `json:"model"`
	Policy        string              `json:"policy"`
	Serving       string              `json:"serving"`
}

func recommendationCursor(s recommendationSnapshot) uuid.UUID {
	return uuid.NewSHA1(s.ID, []byte(fmt.Sprintf("%d|%d", s.PostOffset, s.ReceiptOffset)))
}

func decodeRecommendationSnapshot(payload []byte) (recommendationSnapshot, error) {
	var s recommendationSnapshot
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if len(payload) > 64*1024 || d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF ||
		s.ID == uuid.Nil || len(s.Posts) > 200 || len(s.Receipts) > 200 ||
		s.PostOffset < 0 || s.PostOffset > len(s.Posts) || s.ReceiptOffset < 0 || s.ReceiptOffset > len(s.Receipts) ||
		s.Model == "" || len(s.Model) > 128 || s.Policy == "" || len(s.Policy) > 128 ||
		(s.Serving != "ranked" && s.Serving != "shadow" && s.Serving != "fallback") {
		return s, errors.New("invalid recommendation snapshot")
	}
	seen := map[uuid.UUID]bool{}
	exposures := map[uuid.UUID]bool{}
	for _, ref := range s.Posts {
		if ref.ID == uuid.Nil || ref.Exposure == uuid.Nil || ref.Revision < 1 || seen[ref.ID] || exposures[ref.Exposure] ||
			(ref.Explanation != "RECENT_PUBLIC_POST" && ref.Explanation != "EXPLICIT_INTEREST" && ref.Explanation != "CHOSEN_LOCALITY" && ref.Explanation != "FOLLOWING") {
			return s, errors.New("invalid recommendation reference")
		}
		seen[ref.ID] = true
		exposures[ref.Exposure] = true
	}
	seen = map[uuid.UUID]bool{}
	for _, id := range s.Receipts {
		if id == uuid.Nil || seen[id] {
			return s, errors.New("invalid recommendation receipt")
		}
		seen[id] = true
	}
	return s, nil
}

func (a *App) cachedRecommendationSnapshot(ctx context.Context, scope snapshotcache.Scope) (recommendationSnapshot, time.Time, bool) {
	if a.Snapshots == nil {
		return recommendationSnapshot{}, time.Time{}, false
	}
	record, err := a.Snapshots.Load(ctx, scope)
	status := "hit"
	if errors.Is(err, snapshotcache.ErrMiss) {
		status = "miss"
	} else if err != nil {
		status = "error"
	}
	var s recommendationSnapshot
	if err == nil {
		s, err = decodeRecommendationSnapshot(record.Payload)
		if err == nil && (recommendationCursor(s) != scope.Token || !time.Now().Before(record.Expires)) {
			err = errors.New("cached cursor does not match snapshot")
		}
		if err != nil {
			status = "invalid"
		}
	}
	slog.Info("recommendation snapshot cache", "operation", "load", "status", status)
	return s, record.Expires, err == nil
}

func (a *App) saveRecommendationSnapshot(ctx context.Context, scope snapshotcache.Scope, record snapshotcache.Record) {
	if a.Snapshots == nil {
		return
	}
	status := "saved"
	if a.Snapshots.Save(ctx, scope, record) != nil {
		status = "error"
	}
	slog.Info("recommendation snapshot cache", "operation", "save", "status", status)
}

const recommendationCandidates = `
WITH eligible AS (
 SELECT p.id,p.published_revision,p.author_id,p.published_at,
 'body:'||md5(r.body) AS dedup_key,
 coalesce(p.source_post_id,p.id)::text AS conversation_key,
 CASE WHEN c.slug::text=ANY($2::text[]) OR ($7::boolean AND EXISTS(
 SELECT FROM social.recommendation_event e JOIN social.recommendation_exposure x ON x.id=e.exposure_id
 JOIN social.post previous ON previous.id=x.post_id
 WHERE e.profile_id=$1 AND e.generation=$6 AND e.kind='MORE'
 AND previous.state='PUBLISHED' AND previous.published_revision=x.revision
 AND e.created_at>statement_timestamp()-interval '30 days'
 AND (previous.community_id=p.community_id OR (p.community_id IS NULL AND previous.author_id=p.author_id)))) THEN 1.0 ELSE 0.0 END::double precision AS interest,
 CASE WHEN c.scope_kind='GEOGRAPHIC' AND (lower(c.slug::text)=lower($3) OR lower(c.title)=lower($3)) THEN 1.0 ELSE 0.0 END::double precision AS locality,
 CASE WHEN EXISTS(SELECT FROM social.profile_follow f WHERE f.follower_id=$1 AND f.followed_id=p.author_id)
 OR EXISTS(SELECT FROM social.community_follow f WHERE f.profile_id=$1 AND f.community_id=p.community_id)
 OR EXISTS(SELECT FROM social.community_member m WHERE m.profile_id=$1 AND m.community_id=p.community_id AND m.state='ACTIVE') THEN 1.0 ELSE 0.0 END::double precision AS relationship,
 (1.0/(1.0+GREATEST(0,extract(epoch FROM (statement_timestamp()-p.published_at))/86400)))::double precision AS freshness,
 LEAST(1.0,GREATEST(0,coalesce(s.up_count-s.down_count,0))/20.0)::double precision AS usefulness
 FROM social.post p JOIN social.post_revision r ON r.post_id=p.id AND r.revision=p.published_revision
 JOIN social.profile a ON a.id=p.author_id LEFT JOIN social.community c ON c.id=p.community_id LEFT JOIN social.post_stats s ON s.post_id=p.id
 WHERE p.state='PUBLISHED' AND a.state='ACTIVE' AND r.review_state='APPROVED'
 AND (p.source_post_id IS NULL OR EXISTS(
 SELECT FROM social.post original JOIN social.profile oa ON oa.id=original.author_id LEFT JOIN social.community oc ON oc.id=original.community_id
 WHERE original.id=p.source_post_id AND original.state='PUBLISHED' AND oa.state='ACTIVE'
 AND (oc.id IS NULL OR (oc.state='ACTIVE' AND oc.visibility IN ('PUBLIC','RESTRICTED')))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE (b.blocker_id=$1 AND b.blocked_id=oa.id) OR (b.blocked_id=$1 AND b.blocker_id=oa.id))))
 AND (c.id IS NULL OR (c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED')))
 AND ($4::uuid='00000000-0000-0000-0000-000000000000' OR c.id=$4)
 AND (cardinality($5::text[])=0 OR r.language_tag=ANY($5))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE (b.blocker_id=$1 AND b.blocked_id=p.author_id) OR (b.blocked_id=$1 AND b.blocker_id=p.author_id))
 AND NOT EXISTS(SELECT FROM social.mute m WHERE m.profile_id=$1 AND (m.muted_profile_id=p.author_id OR m.muted_community_id=p.community_id) AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp()))
), sources AS (
 (SELECT id FROM eligible WHERE relationship>0 ORDER BY published_at DESC,id LIMIT 500)
 UNION (SELECT id FROM eligible WHERE interest>0 OR locality>0 ORDER BY published_at DESC,id LIMIT 500)
 UNION (SELECT id FROM eligible ORDER BY published_at DESC,id LIMIT 1000)
)
SELECT id,published_revision,author_id,dedup_key,conversation_key,interest,locality,relationship,freshness,usefulness FROM eligible
WHERE id IN (SELECT id FROM sources) ORDER BY published_at DESC,id LIMIT 2000`

func stickyRecommendation(viewer string, percentage int) bool {
	h := sha256.Sum256([]byte("recommendation-v1|" + viewer))
	bucket := (int(h[0])*256 + int(h[1])) % 10000
	return bucket < percentage*100
}
func (a *App) createRecommendationSnapshot(ctx context.Context, viewer uuid.UUID, binding string, p recommendationPreference, cid uuid.UUID) (recommendationSnapshot, error) {
	s := recommendationSnapshot{ID: uuid.New(), Posts: []recommendationRef{}, Receipts: []uuid.UUID{}, Model: "chronological-v1", Policy: "authorized-fallback-v1", Serving: "fallback"}
	rows, err := a.store(ctx).Query(ctx, recommendationCandidates, viewer, p.Interests, p.Locality, cid, p.Languages, p.Generation, p.PersonalizationEnabled)
	if err != nil {
		return s, err
	}
	candidates := []*pb.Candidate{}
	for rows.Next() {
		var id, author uuid.UUID
		var c pb.Candidate
		if err = rows.Scan(&id, &c.Revision, &author, &c.DedupKey, &c.ConversationKey, &c.ExplicitInterest, &c.Locality, &c.Relationship, &c.Freshness, &c.BoundedUsefulness); err != nil {
			rows.Close()
			return s, err
		}
		c.Id = id.String()
		c.AuthorId = author.String()
		candidates = append(candidates, &c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return s, err
	}
	// Retrieval order is chronological. Fallback and shadow preserve that order.
	ranked := []*pb.RankedReference{}
	seen := map[string]bool{}
	conversations := map[string]bool{}
	for _, c := range candidates {
		if !seen[c.DedupKey] && !conversations[c.ConversationKey] {
			seen[c.DedupKey] = true
			conversations[c.ConversationKey] = true
			ranked = append(ranked, &pb.RankedReference{Id: c.Id, Revision: c.Revision, Explanation: "RECENT_PUBLIC_POST"})
		}
		if len(ranked) == 200 {
			break
		}
	}
	if a.Ranker != nil && a.Config.RecommendationMode != "off" && a.Config.RecommendationMode != "" {
		ctx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
		req := &pb.RecommendRequest{ViewerContext: uuid.NewString(), Generation: p.Generation, Surface: "HOME", Languages: p.Languages, Locality: p.Locality, SnapshotId: s.ID.String(), DeadlineUnixMs: time.Now().Add(120 * time.Millisecond).UnixMilli(), Candidates: candidates, Limit: 200}
		start := time.Now()
		result, e := a.Ranker.Recommend(ctx, req)
		cancel()
		if e == nil {
			e = recommendation.Validate(req, result)
		}
		overlap := 0
		if e == nil {
			top := map[string]bool{}
			for _, item := range ranked[:min(20, len(ranked))] {
				top[item.Id] = true
			}
			for _, item := range result.Items[:min(20, len(result.Items))] {
				if top[item.Id] {
					overlap++
				}
			}
		}
		slog.Info("recommendation", "top20Overlap", overlap, "elapsedMs", time.Since(start).Milliseconds(), "candidates", len(candidates), "success", e == nil, "mode", a.Config.RecommendationMode)
		if e == nil && a.Config.RecommendationMode == "serve" && stickyRecommendation(binding, a.Config.RecommendationRollout) {
			ranked = result.Items
			s.Model = result.ModelVersion
			s.Policy = result.PolicyVersion
			s.Serving = "ranked"
		}
		if e == nil && a.Config.RecommendationMode == "shadow" {
			s.Serving = "shadow"
		}
	}
	for _, ref := range ranked {
		id, e := uuid.Parse(ref.Id)
		if e != nil {
			return s, e
		}
		s.Posts = append(s.Posts, recommendationRef{id, ref.Revision, ref.Explanation, uuid.New()})
	}
	if cid == uuid.Nil {
		rows, err = a.store(ctx).Query(ctx, `SELECT id FROM social.case_receipt WHERE publication_state='PUBLISHED' AND public_state<>'RESOLVED' AND ($1='' OR lower(area_label)=lower($1)) ORDER BY urgency_tier DESC,first_reported_at,id LIMIT 200`, p.Locality)
		if err != nil {
			return s, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return s, err
			}
			s.Receipts = append(s.Receipts, id)
		}
		rows.Close()
		err = rows.Err()
	}
	return s, err
}
func (a *App) recommendedFeed(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	mode := strings.ToUpper(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = "HOME"
	}
	// Explicit Following and operational feeds keep their chronological/urgency contracts.
	if mode != "HOME" {
		clone := r.Clone(r.Context())
		u := *r.URL
		v := u.Query()
		v.Set("sort", "new")
		u.RawQuery = v.Encode()
		clone.URL = &u
		return a.feedPage(clone, actor, false)
	}
	cid := uuid.Nil
	var err error
	if raw := r.URL.Query().Get("communityId"); raw != "" {
		cid, err = uuid.Parse(raw)
		if err != nil {
			return nil, 0, invalid("Invalid community")
		}
	}
	viewer := actorID(actor)
	binding := viewer.String()
	if actor == nil {
		cookie, e := r.Cookie("jansetu_feed_viewer")
		if e != nil || len(cookie.Value) != 36 {
			cookie = &http.Cookie{Name: "jansetu_feed_viewer", Value: uuid.NewString(), Path: "/", HttpOnly: true, Secure: a.Config.SecureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: 86400}
			http.SetCookie(w, cookie)
		}
		hash := sha256.Sum256([]byte(cookie.Value))
		binding = hex.EncodeToString(hash[:])
	}
	query := "HOME|recommended|" + cid.String() + "|" + binding
	p, err := readRecommendationPreference(r.Context(), a.store(r.Context()), viewer)
	if err != nil {
		return nil, 0, err
	}
	var s recommendationSnapshot
	expires := time.Now().Add(5 * time.Minute)
	var readThroughScope snapshotcache.Scope
	var readThroughRecord snapshotcache.Record
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		token, e := uuid.Parse(raw)
		if e != nil {
			return nil, 0, invalid("Invalid feed cursor")
		}
		scope := snapshotcache.Scope{Token: token, Query: query, Generation: p.Generation}
		var hit bool
		s, expires, hit = a.cachedRecommendationSnapshot(r.Context(), scope)
		if !hit {
			var payload []byte
			var generation int64
			err = a.store(r.Context()).QueryRow(r.Context(), `SELECT payload,expires_at,generation FROM social.recommendation_snapshot WHERE id=$1 AND viewer_id=$2 AND query_key=$3`, token, viewer, query).Scan(&payload, &expires, &generation)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, 0, failure(410, "CURSOR_EXPIRED", "Refresh the feed to continue")
			}
			if err != nil {
				return nil, 0, err
			}
			if !time.Now().Before(expires) || generation != p.Generation {
				return nil, 0, failure(410, "CURSOR_EXPIRED", "Refresh the feed to continue")
			}
			if s, err = decodeRecommendationSnapshot(payload); err != nil {
				return nil, 0, err
			}
			readThroughScope = scope
			readThroughRecord = snapshotcache.Record{Payload: payload, Expires: expires}
		}
	} else {
		s, err = a.createRecommendationSnapshot(r.Context(), viewer, binding, p, cid)
		if err != nil {
			return nil, 0, err
		}
	}
	if len(s.Posts) > 200 || len(s.Receipts) > 200 || s.PostOffset < 0 || s.PostOffset > len(s.Posts) || s.ReceiptOffset < 0 || s.ReceiptOffset > len(s.Receipts) {
		return nil, 0, invalid("Invalid snapshot")
	}
	items := []any{}
	var next any
	var pendingScope snapshotcache.Scope
	var pendingRecord snapshotcache.Record
	hydrate := func(tx pgx.Tx) error {
		current, e := readRecommendationPreference(r.Context(), tx, viewer)
		if e != nil {
			return e
		}
		if current.Generation != p.Generation {
			return failure(410, "CURSOR_EXPIRED", "Refresh the feed to continue")
		}
		if !time.Now().Before(expires) {
			return failure(410, "CURSOR_EXPIRED", "Refresh the feed to continue")
		}
		ids := []uuid.UUID{}
		for _, ref := range s.Posts[s.PostOffset:] {
			ids = append(ids, ref.ID)
		}
		posts, e := dbgen.New(tx).RecommendationPosts(r.Context(), dbgen.RecommendationPostsParams{ViewerID: viewer, PostIds: ids})
		if e != nil {
			return e
		}
		type post struct {
			ID                uuid.UUID
			PublishedRevision int32
			Author            *struct{ ID uuid.UUID }
		}
		bodies := map[uuid.UUID]json.RawMessage{}
		authors := map[uuid.UUID]uuid.UUID{}
		for _, data := range posts {
			var post post
			if e = json.Unmarshal(data, &post); e != nil {
				return e
			}
			if post.Author != nil {
				authors[post.ID] = post.Author.ID
			}
			bodies[post.ID] = data
		}
		hidden := map[uuid.UUID]bool{}
		if p.PersonalizationEnabled {
			rows, e := tx.Query(r.Context(), `SELECT x.post_id FROM social.recommendation_event e JOIN social.recommendation_exposure x ON x.id=e.exposure_id WHERE e.profile_id=$1 AND e.generation=$2 AND e.kind IN ('LESS','SKIP')`, viewer, p.Generation)
			if e != nil {
				return e
			}
			for rows.Next() {
				var id uuid.UUID
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return e
				}
				hidden[id] = true
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				return e
			}
		}
		receiptBodies := map[uuid.UUID]json.RawMessage{}
		rows, e := tx.Query(r.Context(), `SELECT r.id,jsonb_build_object('id',r.id,'title',r.title,'summary',r.safe_summary,'area',r.area_label,'state',r.public_state,'urgencyTier',r.urgency_tier,'firstReportedAt',r.first_reported_at,'updatedAt',r.updated_at,'nextUpdateDueAt',r.next_update_due_at,'responsibilities',r.responsibilities,'version',r.publication_version,'following',EXISTS(SELECT FROM social.case_follow f WHERE f.profile_id=$2 AND f.receipt_id=r.id)) FROM social.case_receipt r WHERE r.id=ANY($1::uuid[]) AND r.publication_state='PUBLISHED' AND r.public_state<>'RESOLVED'`, s.Receipts[s.ReceiptOffset:], viewer)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id uuid.UUID
			var data []byte
			if e = rows.Scan(&id, &data); e != nil {
				rows.Close()
				return e
			}
			receiptBodies[id] = data
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		addReceipt := func() bool {
			for s.ReceiptOffset < len(s.Receipts) {
				id := s.Receipts[s.ReceiptOffset]
				s.ReceiptOffset++
				if data, ok := receiptBodies[id]; ok {
					items = append(items, map[string]any{"type": "CASE_RECEIPT", "receipt": data, "section": "CIVIC_UPDATES", "recommendation": map[string]any{"explanation": "CIVIC_URGENCY"}})
					return true
				}
			}
			return false
		}
		if cid == uuid.Nil {
			for i := 0; i < 6; i++ {
				if !addReceipt() {
					break
				}
			}
		}
		counts := map[uuid.UUID]int{}
		for s.PostOffset < len(s.Posts) && len(items) < 20 {
			ref := s.Posts[s.PostOffset]
			s.PostOffset++
			data, ok := bodies[ref.ID]
			if !ok || hidden[ref.ID] || counts[authors[ref.ID]] >= 2 {
				continue
			}
			var post post
			if e = json.Unmarshal(data, &post); e != nil {
				return e
			}
			if post.PublishedRevision != ref.Revision {
				continue
			}
			counts[authors[ref.ID]]++
			explanation := map[string]any{"explanation": ref.Explanation}
			if actor != nil && p.PersonalizationEnabled {
				_, e = tx.Exec(r.Context(), `INSERT INTO social.recommendation_exposure(id,profile_id,post_id,revision,generation,model_version,policy_version,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, ref.Exposure, viewer, ref.ID, ref.Revision, p.Generation, s.Model, s.Policy, expires)
				if e != nil {
					return e
				}
				explanation["exposureId"] = ref.Exposure
			}
			items = append(items, map[string]any{"type": "POST", "post": data, "recommendation": explanation})
		}
		for len(items) < 20 && addReceipt() {
		}
		if s.PostOffset < len(s.Posts) || s.ReceiptOffset < len(s.Receipts) {
			token := recommendationCursor(s)
			payload := jsonBytes(s)
			_, e = tx.Exec(r.Context(), `INSERT INTO social.recommendation_snapshot(id,viewer_id,query_key,generation,expires_at,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, token, viewer, query, p.Generation, expires, payload)
			if e != nil {
				return e
			}
			if a.Snapshots != nil {
				pendingScope = snapshotcache.Scope{Token: token, Query: query, Generation: p.Generation}
				pendingRecord = snapshotcache.Record{Payload: payload, Expires: expires}
			}
			next = token.String()
		}
		return nil
	}
	if actor != nil {
		err = a.recommendationTransaction(r.Context(), actor, hydrate)
	} else {
		var tx pgx.Tx
		tx, err = a.begin(r.Context())
		if err == nil {
			defer tx.Rollback(r.Context())
			err = hydrate(tx)
			if err == nil {
				err = tx.Commit(r.Context())
			}
		}
	}
	if err != nil {
		return nil, 0, err
	}
	// Cache writes happen only after exposures and the durable child cursor commit.
	if readThroughScope.Token != uuid.Nil {
		a.saveRecommendationSnapshot(r.Context(), readThroughScope, readThroughRecord)
	}
	if pendingScope.Token != uuid.Nil {
		a.saveRecommendationSnapshot(r.Context(), pendingScope, pendingRecord)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": expires.UTC().Format(time.RFC3339), "mode": mode, "recommendationMode": s.Serving}, 200, nil
}
