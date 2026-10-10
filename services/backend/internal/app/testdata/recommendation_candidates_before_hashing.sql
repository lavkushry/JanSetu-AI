WITH recent_interests AS MATERIALIZED (
 SELECT DISTINCT previous.community_id,previous.author_id
 FROM social.recommendation_event e JOIN social.recommendation_exposure x ON x.id=e.exposure_id
 JOIN social.post previous ON previous.id=x.post_id
 WHERE $7::boolean AND e.profile_id=$1 AND e.generation=$6 AND e.kind='MORE'
 AND previous.state='PUBLISHED' AND previous.published_revision=x.revision
 AND e.created_at>statement_timestamp()-interval '30 days'
), viewer_mutes AS MATERIALIZED (
 SELECT muted_profile_id,muted_community_id FROM social.mute
 WHERE profile_id=$1 AND (expires_at IS NULL OR expires_at>statement_timestamp())
), eligible AS (
 SELECT p.id,p.published_revision,p.author_id,p.published_at,
 'body:'||md5(r.body) AS dedup_key,
 coalesce(p.source_post_id,p.id)::text AS conversation_key,
 CASE WHEN c.slug::text=ANY($2::text[]) OR EXISTS(
 SELECT FROM recent_interests previous
 WHERE previous.community_id=p.community_id OR (p.community_id IS NULL AND previous.author_id=p.author_id)) THEN 1.0 ELSE 0.0 END::double precision AS interest,
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
 AND NOT EXISTS(SELECT FROM viewer_mutes m WHERE m.muted_profile_id=p.author_id OR m.muted_community_id=p.community_id)
), sources AS (
 (SELECT id FROM eligible WHERE relationship>0 ORDER BY published_at DESC,id LIMIT 500)
 UNION (SELECT id FROM eligible WHERE interest>0 OR locality>0 ORDER BY published_at DESC,id LIMIT 500)
 UNION (SELECT id FROM eligible ORDER BY published_at DESC,id LIMIT 1000)
)
SELECT id,published_revision,author_id,dedup_key,conversation_key,interest,locality,relationship,freshness,usefulness FROM eligible
WHERE id IN (SELECT id FROM sources) ORDER BY published_at DESC,id LIMIT 2000
