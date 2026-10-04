-- Synthetic development fixtures. Re-running this seed never resets user-created work.
BEGIN;
INSERT INTO social.profile(id,handle,display_name,bio,state) VALUES
('20000000-0000-4000-8000-000000000001','ananya','Ananya Rao','Making our neighbourhood a little more walkable.','ACTIVE'),
('20000000-0000-4000-8000-000000000002','rohan','Rohan Mehta','Cyclist, coffee enthusiast, local optimist.','ACTIVE'),
('20000000-0000-4000-8000-000000000003','maya','Maya Iyer','Small actions. Better streets.','ACTIVE'),
('20000000-0000-4000-8000-000000000004','coordinator','Kiran Shah','Synthetic district coordination account.','ACTIVE'),
('20000000-0000-4000-8000-000000000005','cityworks','City Works team','Synthetic agency account.','ACTIVE'),
('20000000-0000-4000-8000-000000000006','verifier','Neha Sen','Synthetic independent verification account.','ACTIVE') ON CONFLICT DO NOTHING;
INSERT INTO identity.principal(id,profile_id,state)
SELECT ('10000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,('20000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,'ACTIVE'
FROM generate_series(1,6) n ON CONFLICT DO NOTHING;
-- Exact local issuer/immutable subject bindings; names and email never bind staff accounts.
INSERT INTO identity.account_binding(provider,provider_subject,principal_id,state)
SELECT 'http://localhost:8180/realms/jansetu',
('90000000-0000-4000-8000-'||lpad(n::text,12,'0')),
('10000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,'ACTIVE'
FROM unnest(ARRAY[1,2,4,5,6]) n ON CONFLICT DO NOTHING;
INSERT INTO ops.agency(id,name,organization_type,state) VALUES
('30000000-0000-4000-8000-000000000001','City Works (demo)','MUNICIPAL','ACTIVE'),
('30000000-0000-4000-8000-000000000002','Water Services (demo)','UTILITY','ACTIVE') ON CONFLICT DO NOTHING;
INSERT INTO identity.platform_grant(id,principal_id,role,valid_to) VALUES
('40000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000004','COORDINATOR',now()+interval '5 years'),
('40000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000004','PLATFORM_MODERATOR',now()+interval '5 years'),
('40000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000004','PUBLISHER',now()+interval '5 years') ON CONFLICT DO NOTHING;
INSERT INTO identity.organization_grant(id,principal_id,agency_id,role,valid_from,valid_to) VALUES
('40000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000005','30000000-0000-4000-8000-000000000001','AGENCY_AGENT',now()-interval '1 day',now()+interval '5 years'),
('40000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000006','30000000-0000-4000-8000-000000000001','VERIFIER',now()-interval '1 day',now()+interval '5 years') ON CONFLICT DO NOTHING;
INSERT INTO social.community(id,slug,title,description,scope_kind,visibility,rules_body,state,language_tag) VALUES
('50000000-0000-4000-8000-000000000001','indiranagar','Indiranagar','For the streets we share, and the people who make them better.','TOPIC','PUBLIC','Be kind. Share observations honestly. Keep personal information private.','ACTIVE','en-IN'),
('50000000-0000-4000-8000-000000000002','koramangala','Koramangala','Better connections, cleaner corners, everyday local life.','TOPIC','PUBLIC','Stay constructive. Cite sources. Never publish private identities.','ACTIVE','en-IN'),
('50000000-0000-4000-8000-000000000003','bengaluru-cycling','Bengaluru Cycling','Safer routes and conversations on two wheels.','TOPIC','PUBLIC','Be helpful. No harassment. Keep route advice grounded.','ACTIVE','en-IN'),
('50000000-0000-4000-8000-000000000004','green-city','A greener Bengaluru','Small steps for a city that breathes easier.','TOPIC','PUBLIC','Share practical information and respect differing views.','ACTIVE','en-IN') ON CONFLICT DO NOTHING;
INSERT INTO social.community_member(community_id,profile_id,role,state)
SELECT c.id,p.id,'MEMBER','ACTIVE' FROM social.community c CROSS JOIN social.profile p ON CONFLICT DO NOTHING;
INSERT INTO social.community_follow(profile_id,community_id) VALUES
('20000000-0000-4000-8000-000000000001','50000000-0000-4000-8000-000000000001'),
('20000000-0000-4000-8000-000000000001','50000000-0000-4000-8000-000000000003') ON CONFLICT DO NOTHING;

INSERT INTO social.post(id,author_id,community_id,kind,state,created_at) VALUES
('60000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000003','50000000-0000-4000-8000-000000000001','DISCUSSION','PENDING',now()-interval '2 hours'),
('60000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000002','50000000-0000-4000-8000-000000000003','QUESTION','PENDING',now()-interval '4 hours'),
('60000000-0000-4000-8000-000000000003','20000000-0000-4000-8000-000000000001','50000000-0000-4000-8000-000000000004','SHORT','PENDING',now()-interval '6 hours'),
('60000000-0000-4000-8000-000000000004','20000000-0000-4000-8000-000000000003','50000000-0000-4000-8000-000000000002','DISCUSSION','PENDING',now()-interval '8 hours') ON CONFLICT DO NOTHING;
INSERT INTO social.post_revision(post_id,revision,title,body,language_tag,review_state,editor_id) VALUES
('60000000-0000-4000-8000-000000000001',1,'What would a more walkable 12th Main look like?',E'The tree-lined stretch is one of my favourite parts of the neighbourhood. But the last 200 metres before the junction are difficult with a stroller or wheelchair.\n\nWould love to collect specific observations and practical ideas from everyone who uses this route.','en-IN','APPROVED','20000000-0000-4000-8000-000000000003'),
('60000000-0000-4000-8000-000000000002',1,'Your favourite quiet cycling route this weekend?','Looking for a beginner-friendly loop, ideally with less traffic and a good breakfast stop. Share a route you have actually ridden!','en-IN','APPROVED','20000000-0000-4000-8000-000000000002'),
('60000000-0000-4000-8000-000000000003',1,NULL,'A small win for the weekend: our community composting meetup is happening on Sunday at 9 am. Bring your questions and a reusable cup. Everyone is welcome. 🌱','en-IN','APPROVED','20000000-0000-4000-8000-000000000001'),
('60000000-0000-4000-8000-000000000004',1,'A little appreciation for the people keeping our streets clean','The early-morning cleaning team on our lane does an incredible job. What are some small things we can do as residents to make their work easier?','en-IN','APPROVED','20000000-0000-4000-8000-000000000003') ON CONFLICT DO NOTHING;
UPDATE social.post SET state='PUBLISHED',published_revision=1,published_at=created_at
WHERE id::text LIKE '60000000-%' AND state='PENDING' AND version=1 AND current_revision=1;
-- Repair only the original synthetic fixture's escaped paragraph separators.
UPDATE social.post_revision SET body=replace(body,chr(92)||'n',chr(10))
WHERE post_id='60000000-0000-4000-8000-000000000001' AND revision=1
AND strpos(body,chr(92)||'n'||chr(92)||'nWould love to collect specific observations')>0;
INSERT INTO social.comment(id,post_id,author_id,body,depth,state) VALUES
('61000000-0000-4000-8000-000000000001','60000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000002','The uneven paving near the bus stop is another spot worth documenting. Happy to walk the route together.',0,'PENDING'),
('61000000-0000-4000-8000-000000000002','60000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000003','Try the park loop early in the morning. It is short, shaded and a comfortable pace for beginners.',0,'PENDING') ON CONFLICT DO NOTHING;
INSERT INTO social.comment_revision(comment_id,version,body,language_tag,review_state)
SELECT id,1,body,'en-IN','APPROVED' FROM social.comment WHERE id::text LIKE '61000000-%' ON CONFLICT DO NOTHING;
UPDATE social.comment SET state='PUBLISHED',published_version=1 WHERE id::text LIKE '61000000-%' AND state='PENDING' AND version=1;
INSERT INTO social.post_vote(profile_id,post_id,value) VALUES
('20000000-0000-4000-8000-000000000001','60000000-0000-4000-8000-000000000001',1),
('20000000-0000-4000-8000-000000000002','60000000-0000-4000-8000-000000000001',1),
('20000000-0000-4000-8000-000000000003','60000000-0000-4000-8000-000000000002',1) ON CONFLICT DO NOTHING;
INSERT INTO social.post_stats(post_id,up_count,down_count,comment_count,repost_count,as_of)
SELECT p.id,(SELECT count(*) FROM social.post_vote v WHERE v.post_id=p.id AND value=1),0,(SELECT count(*) FROM social.comment c WHERE c.post_id=p.id AND state='PUBLISHED'),0,now()
FROM social.post p WHERE id::text LIKE '60000000-%' ON CONFLICT DO NOTHING;

INSERT INTO ops.case_record(id,category_code,state,first_valid_report_at,urgency_tier) VALUES
('70000000-0000-4000-8000-000000000001','FOOTPATH_ACCESS','ACTIVE',now()-interval '3 days',2),
('70000000-0000-4000-8000-000000000002','STREET_LIGHT','OPEN',now()-interval '1 day',1),
('70000000-0000-4000-8000-000000000003','WASTE','RESOLVED',now()-interval '7 days',0) ON CONFLICT DO NOTHING;
INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state,authority_basis_ref,accepted_at,work_summary) VALUES
('71000000-0000-4000-8000-000000000001','70000000-0000-4000-8000-000000000001','30000000-0000-4000-8000-000000000001','RESTORATION','ACCEPTED','synthetic-local-mandate-v1',now()-interval '2 days','Inspect the damaged pedestrian crossing and restore access.'),
('71000000-0000-4000-8000-000000000002','70000000-0000-4000-8000-000000000002','30000000-0000-4000-8000-000000000001','RESTORATION','PROPOSED','synthetic-local-mandate-v1',NULL,'Inspect the non-working lights.'),
('71000000-0000-4000-8000-000000000003','70000000-0000-4000-8000-000000000003','30000000-0000-4000-8000-000000000001','RESTORATION','VERIFIED','synthetic-local-mandate-v1',now()-interval '6 days','Collection point cleared and independently inspected.') ON CONFLICT DO NOTHING;
INSERT INTO social.case_receipt(id,title,safe_summary,area_label,public_state,urgency_tier,first_reported_at,projection_version,publication_state,published_at,updated_at,policy_version) VALUES
('72000000-0000-4000-8000-000000000001','A safer crossing near the community park','Uneven paving is making this crossing difficult to use. City Works has accepted the inspection and restoration task.','Indiranagar · Bengaluru','ACTIVE',2,now()-interval '3 days',1,'PUBLISHED',now()-interval '3 days',now()-interval '2 days','local-publication-v1'),
('72000000-0000-4000-8000-000000000002','Street lights on 7th Cross need attention','Residents have observed a dark stretch along the evening walking route. The proposed inspection is awaiting acceptance.','Koramangala · Bengaluru','OPEN',1,now()-interval '1 day',1,'PUBLISHED',now()-interval '1 day',now()-interval '1 day','local-publication-v1'),
('72000000-0000-4000-8000-000000000003','The collection point is clear again','The collection point has been cleared. The synthetic fixture includes an independent field inspection and restoration decision.','Indiranagar · Bengaluru','RESOLVED',0,now()-interval '7 days',1,'PUBLISHED',now()-interval '7 days',now()-interval '1 day','local-publication-v1') ON CONFLICT DO NOTHING;
UPDATE social.case_receipt r SET responsibilities=jsonb_build_array(jsonb_build_object('agency','City Works (demo)','state',v.task_state,'dueAt',NULL))
FROM (VALUES ('72000000-0000-4000-8000-000000000001'::uuid,'ACCEPTED'),('72000000-0000-4000-8000-000000000002'::uuid,'PROPOSED'),('72000000-0000-4000-8000-000000000003'::uuid,'VERIFIED')) AS v(id,task_state)
WHERE r.id=v.id AND r.responsibilities='[]' AND r.projection_version=1;
INSERT INTO ops.publication_binding(case_id,receipt_id,approved_case_version,reviewer_ref,decision_ref,approved_at)
SELECT c.id,('72000000-0000-4000-8000-'||right(c.id::text,12))::uuid,1,'10000000-0000-4000-8000-000000000004','synthetic-initial-publication',now()
FROM ops.case_record c WHERE c.id::text LIKE '70000000-%' ON CONFLICT DO NOTHING;
INSERT INTO social.case_receipt_event(receipt_id,sequence,type,safe_text,actor_type,occurred_at,projection_version)
SELECT id,1,'RECEIVED','The observation was recorded in this synthetic demonstration.','PLATFORM',first_reported_at,1
FROM social.case_receipt WHERE id::text LIKE '72000000-%' ON CONFLICT DO NOTHING;
INSERT INTO social.case_receipt_event(receipt_id,sequence,type,safe_text,actor_type,occurred_at,projection_version) VALUES
('72000000-0000-4000-8000-000000000001',2,'ACCEPTED','City Works accepted the inspection and restoration task.','AGENCY',now()-interval '2 days',1),
('72000000-0000-4000-8000-000000000003',2,'VERIFIED','An independent reviewer recorded the synthetic restoration decision.','REVIEWER',now()-interval '1 day',1) ON CONFLICT DO NOTHING;
COMMIT;
