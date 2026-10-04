-- Generated from the approved v3 reference schema; maintained as a versioned migration.
-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE SCHEMA social;
CREATE SCHEMA ops;
CREATE SCHEMA geo;
CREATE SCHEMA infra;

CREATE TABLE social.profile (
  id uuid PRIMARY KEY,
  handle citext NOT NULL UNIQUE,
  display_name text NOT NULL,
  bio text NOT NULL DEFAULT '',
  state text NOT NULL CHECK (state IN ('ACTIVE','SUSPENDED','DEACTIVATED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  version bigint NOT NULL DEFAULT 1,
  CHECK (char_length(handle::text) BETWEEN 3 AND 30),
  CHECK (char_length(display_name) BETWEEN 1 AND 80),
  CHECK (char_length(bio) <= 500)
);

CREATE TABLE social.community (
  id uuid PRIMARY KEY,
  slug citext NOT NULL UNIQUE,
  title text NOT NULL,
  scope_kind text NOT NULL CHECK (scope_kind IN ('GEOGRAPHIC','TOPIC')),
  admin_unit_id uuid,
  visibility text NOT NULL CHECK (visibility IN ('PUBLIC','RESTRICTED','PRIVATE')),
  rules_revision integer NOT NULL DEFAULT 1,
  rules_body text NOT NULL,
  state text NOT NULL CHECK (state IN ('ACTIVE','FROZEN','ARCHIVED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  version bigint NOT NULL DEFAULT 1,
  CHECK (scope_kind <> 'GEOGRAPHIC' OR admin_unit_id IS NOT NULL)
);

CREATE TABLE social.community_member (
  community_id uuid NOT NULL REFERENCES social.community(id),
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  role text NOT NULL CHECK (role IN ('MEMBER','CONTRIBUTOR','MODERATOR','OWNER')),
  state text NOT NULL CHECK (state IN ('ACTIVE','PENDING','BANNED','LEFT')),
  joined_at timestamptz NOT NULL DEFAULT now(),
  version bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (community_id, profile_id)
);

CREATE TABLE social.profile_follow (
  follower_id uuid NOT NULL REFERENCES social.profile(id),
  followed_id uuid NOT NULL REFERENCES social.profile(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (follower_id, followed_id),
  CHECK (follower_id <> followed_id)
);

CREATE TABLE social.profile_block (
  blocker_id uuid NOT NULL REFERENCES social.profile(id),
  blocked_id uuid NOT NULL REFERENCES social.profile(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (blocker_id, blocked_id),
  CHECK (blocker_id <> blocked_id)
);

CREATE TABLE social.community_follow (
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  community_id uuid NOT NULL REFERENCES social.community(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (profile_id, community_id)
);

CREATE TABLE social.case_receipt (
  id uuid PRIMARY KEY,
  title text NOT NULL,
  safe_summary text NOT NULL,
  area_label text NOT NULL,
  public_state text NOT NULL,
  urgency_tier smallint NOT NULL CHECK (urgency_tier BETWEEN 0 AND 3),
  first_reported_at timestamptz NOT NULL,
  next_update_due_at timestamptz,
  responsibilities jsonb NOT NULL DEFAULT '[]',
  rank_features jsonb NOT NULL DEFAULT '{}',
  projection_version bigint NOT NULL CHECK (projection_version > 0),
  publication_state text NOT NULL
    CHECK (publication_state IN ('PUBLISHED','WITHDRAWN')),
  published_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  policy_version text NOT NULL
);

CREATE TABLE social.case_follow (
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  receipt_id uuid NOT NULL REFERENCES social.case_receipt(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (profile_id, receipt_id)
);

CREATE TABLE social.post (
  id uuid PRIMARY KEY,
  author_id uuid REFERENCES social.profile(id),
  community_id uuid REFERENCES social.community(id),
  kind text NOT NULL CHECK
    (kind IN ('SHORT','DISCUSSION','QUESTION','PLAYBOOK','QUOTE','CASE_UPDATE')),
  state text NOT NULL CHECK
    (state IN ('DRAFT','PENDING','PUBLISHED','HIDDEN','DELETED')),
  source_post_id uuid REFERENCES social.post(id),
  receipt_id uuid REFERENCES social.case_receipt(id),
  current_revision integer NOT NULL DEFAULT 1,
  published_revision integer,
  created_at timestamptz NOT NULL DEFAULT now(),
  published_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  version bigint NOT NULL DEFAULT 1,
  CHECK ((kind = 'QUOTE') = (source_post_id IS NOT NULL)),
  CHECK (source_post_id IS NULL OR source_post_id <> id),
  CHECK (
    (kind = 'CASE_UPDATE' AND author_id IS NULL AND receipt_id IS NOT NULL)
    OR (kind <> 'CASE_UPDATE' AND author_id IS NOT NULL)
  ),
  CHECK (kind NOT IN ('DISCUSSION','QUESTION','PLAYBOOK')
         OR community_id IS NOT NULL),
  CHECK (state <> 'PUBLISHED'
         OR (published_revision IS NOT NULL AND published_at IS NOT NULL))
);

CREATE TABLE social.post_revision (
  post_id uuid NOT NULL REFERENCES social.post(id),
  revision integer NOT NULL CHECK (revision > 0),
  title text,
  body text NOT NULL,
  language_tag text NOT NULL,
  review_state text NOT NULL CHECK
    (review_state IN ('PENDING','APPROVED','REJECTED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  editor_id uuid REFERENCES social.profile(id),
  PRIMARY KEY (post_id, revision),
  CHECK (title IS NULL OR char_length(title) <= 180),
  CHECK (char_length(body) <= 8000)
);

ALTER TABLE social.post ADD CONSTRAINT post_current_revision_fk
  FOREIGN KEY (id, current_revision)
  REFERENCES social.post_revision(post_id, revision)
  DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE social.post ADD CONSTRAINT post_published_revision_fk
  FOREIGN KEY (id, published_revision)
  REFERENCES social.post_revision(post_id, revision)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE social.comment (
  id uuid PRIMARY KEY,
  post_id uuid NOT NULL REFERENCES social.post(id),
  parent_id uuid,
  author_id uuid NOT NULL REFERENCES social.profile(id),
  body text NOT NULL,
  depth smallint NOT NULL CHECK (depth BETWEEN 0 AND 20),
  state text NOT NULL CHECK (state IN ('PENDING','PUBLISHED','HIDDEN','DELETED')),
  version bigint NOT NULL DEFAULT 1,
  current_revision bigint NOT NULL DEFAULT 1,
  published_version bigint,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (post_id, id),
  FOREIGN KEY (post_id, parent_id) REFERENCES social.comment(post_id, id),
  CHECK (parent_id IS NULL OR parent_id <> id),
  CHECK ((parent_id IS NULL AND depth = 0)
         OR (parent_id IS NOT NULL AND depth > 0)),
  CHECK (char_length(body) <= 4000)
);

CREATE TABLE social.comment_revision (
  comment_id uuid NOT NULL REFERENCES social.comment(id),
  version bigint NOT NULL CHECK (version > 0),
  body text NOT NULL,
  language_tag text NOT NULL,
  review_state text NOT NULL CHECK (review_state IN ('PENDING','APPROVED','REJECTED')),
  changed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (comment_id, version)
);

ALTER TABLE social.comment ADD CONSTRAINT comment_published_version_fk
  FOREIGN KEY (id, published_version)
  REFERENCES social.comment_revision(comment_id, version)
  DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE social.comment ADD CONSTRAINT comment_published_version_required
  CHECK (state <> 'PUBLISHED' OR published_version IS NOT NULL);
ALTER TABLE social.comment ADD CONSTRAINT comment_current_revision_fk
  FOREIGN KEY (id, current_revision)
  REFERENCES social.comment_revision(comment_id, version)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE social.post_vote (
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  post_id uuid NOT NULL REFERENCES social.post(id),
  value smallint NOT NULL CHECK (value IN (-1,1)),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (profile_id, post_id)
);

CREATE TABLE social.comment_vote (
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  comment_id uuid NOT NULL REFERENCES social.comment(id),
  value smallint NOT NULL CHECK (value IN (-1,1)),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (profile_id, comment_id)
);

CREATE TABLE social.repost (
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  post_id uuid NOT NULL REFERENCES social.post(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (profile_id, post_id)
);

CREATE TABLE social.bookmark (
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  post_id uuid NOT NULL REFERENCES social.post(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (profile_id, post_id)
);

CREATE TABLE social.media_asset (
  id uuid PRIMARY KEY,
  uploader_id uuid REFERENCES social.profile(id),
  report_alias_ref uuid,
  storage_key text NOT NULL UNIQUE,
  declared_type text NOT NULL,
  actual_type text,
  byte_count bigint CHECK (byte_count > 0),
  sha256 bytea CHECK (sha256 IS NULL OR octet_length(sha256) = 32),
  state text NOT NULL CHECK
    (state IN ('UPLOADING','QUARANTINED','APPROVED','REJECTED','REVOKED')),
  derivative_key text,
  authorization_version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (num_nonnulls(uploader_id, report_alias_ref) = 1),
  CHECK (state <> 'APPROVED' OR derivative_key IS NOT NULL)
);

CREATE TABLE social.post_media (
  post_id uuid NOT NULL,
  revision integer NOT NULL,
  media_id uuid NOT NULL REFERENCES social.media_asset(id),
  ordinal smallint NOT NULL CHECK (ordinal BETWEEN 0 AND 3),
  alt_text text NOT NULL DEFAULT '',
  PRIMARY KEY (post_id, revision, ordinal),
  UNIQUE (post_id, revision, media_id),
  FOREIGN KEY (post_id, revision)
    REFERENCES social.post_revision(post_id, revision)
);

CREATE INDEX post_community_new_idx
  ON social.post (community_id, published_at DESC, id DESC)
  WHERE state = 'PUBLISHED';
CREATE INDEX post_author_new_idx
  ON social.post (author_id, published_at DESC, id DESC)
  WHERE state = 'PUBLISHED';
CREATE INDEX comment_page_idx
  ON social.comment (post_id, parent_id, created_at, id);
CREATE INDEX followers_reverse_idx
  ON social.profile_follow (followed_id, follower_id);
CREATE INDEX blocks_reverse_idx
  ON social.profile_block (blocked_id, blocker_id);


CREATE TABLE ops.agency (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  organization_type text NOT NULL,
  state text NOT NULL CHECK (state IN ('ACTIVE','PAUSED','RETIRED')),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ops.case_record (
  id uuid PRIMARY KEY,
  category_code text NOT NULL,
  state text NOT NULL CHECK
    (state IN ('OPEN','ACTIVE','VERIFICATION_PENDING','RESOLVED',
               'REOPENED','REFERRED','WITHDRAWN')),
  first_valid_report_at timestamptz NOT NULL,
  operational_location geography(Point,4326),
  accuracy_m integer CHECK (accuracy_m IS NULL OR accuracy_m >= 0),
  urgency_tier smallint NOT NULL CHECK (urgency_tier BETWEEN 0 AND 3),
  urgency_review_ref uuid,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ops.report (
  id uuid PRIMARY KEY,
  client_submission_id uuid NOT NULL UNIQUE,
  reporter_ref uuid,
  source_channel text NOT NULL,
  language_tag text NOT NULL,
  statement text NOT NULL,
  observed_at timestamptz,
  received_at timestamptz NOT NULL DEFAULT now(),
  classification text NOT NULL CHECK (classification = 'PUBLIC_SERVICE'),
  publication_preference text NOT NULL
    CHECK (publication_preference IN ('PRIVATE','SANITIZED_RECEIPT')),
  intake_metadata jsonb NOT NULL DEFAULT '{}',
  retention_policy_id text NOT NULL
);

CREATE TABLE ops.case_observation (
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  report_id uuid NOT NULL REFERENCES ops.report(id),
  relation text NOT NULL CHECK (relation IN ('INITIAL','SUPPORTING','CHALLENGE')),
  linked_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (case_id, report_id)
);

CREATE TABLE ops.evidence (
  id uuid PRIMARY KEY,
  report_id uuid NOT NULL REFERENCES ops.report(id),
  storage_key text NOT NULL UNIQUE,
  content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256) = 32),
  captured_at timestamptz,
  received_at timestamptz NOT NULL DEFAULT now(),
  access_class text NOT NULL,
  retention_policy_id text NOT NULL
);

CREATE TABLE ops.obligation (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  agency_id uuid REFERENCES ops.agency(id),
  obligation_type text NOT NULL,
  state text NOT NULL CHECK
    (state IN ('PROPOSED','DELIVERED','ACKNOWLEDGED','ACCEPTED','DISPUTED',
               'IN_PROGRESS','COMPLETION_CLAIMED','VERIFIED','CANCELLED')),
  authority_basis_ref text,
  due_at timestamptz,
  accepted_at timestamptz,
  completed_at timestamptz,
  version bigint NOT NULL DEFAULT 1,
  CHECK (state NOT IN ('ACCEPTED','IN_PROGRESS','COMPLETION_CLAIMED','VERIFIED')
         OR (agency_id IS NOT NULL AND accepted_at IS NOT NULL))
);

CREATE TABLE ops.case_event (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  sequence bigint NOT NULL CHECK (sequence > 0),
  event_type text NOT NULL,
  actor_ref uuid NOT NULL,
  actor_role text NOT NULL,
  payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (case_id, sequence)
);

CREATE TABLE ops.publication_binding (
  case_id uuid PRIMARY KEY REFERENCES ops.case_record(id),
  receipt_id uuid NOT NULL UNIQUE REFERENCES social.case_receipt(id),
  approved_case_version bigint NOT NULL,
  reviewer_ref uuid NOT NULL,
  decision_ref text NOT NULL,
  approved_at timestamptz NOT NULL
);

CREATE TABLE infra.outbox (
  id uuid PRIMARY KEY,
  aggregate_type text NOT NULL,
  aggregate_id uuid NOT NULL,
  aggregate_version bigint NOT NULL,
  event_type text NOT NULL,
  payload_version integer NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  available_at timestamptz NOT NULL DEFAULT now(),
  lease_until timestamptz,
  lease_owner text,
  lease_token uuid,
  delivered_at timestamptz,
  dead_lettered_at timestamptz,
  attempts integer NOT NULL DEFAULT 0,
  last_error_code text
);

CREATE TABLE infra.processed_event (
  consumer_name text NOT NULL,
  event_id uuid NOT NULL,
  processed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer_name, event_id)
);

CREATE TABLE infra.idempotency_record (
  principal_ref uuid NOT NULL,
  operation text NOT NULL,
  idempotency_key text NOT NULL,
  request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
  result_resource_id uuid,
  response_code integer NOT NULL CHECK (response_code BETWEEN 200 AND 299),
  response_body jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (principal_ref, operation, idempotency_key)
);

CREATE INDEX obligation_queue_idx
  ON ops.obligation (agency_id, state, due_at, id);
CREATE INDEX case_location_idx
  ON ops.case_record USING gist (operational_location);
CREATE INDEX outbox_due_idx
  ON infra.outbox (available_at, created_at)
  WHERE delivered_at IS NULL;


CREATE TABLE geo.release (
  id uuid PRIMARY KEY,
  status text NOT NULL CHECK (status IN ('DRAFT','ACTIVE','RETIRED')),
  source_manifest jsonb NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  approved_at timestamptz,
  approved_by uuid
);

CREATE TABLE geo.admin_unit (
  id uuid PRIMARY KEY,
  country_code char(2) NOT NULL,
  registry_name text,
  registry_code text,
  UNIQUE (country_code, registry_name, registry_code)
);

CREATE TABLE geo.unit_version (
  id uuid PRIMARY KEY,
  release_id uuid NOT NULL REFERENCES geo.release(id),
  unit_id uuid NOT NULL REFERENCES geo.admin_unit(id),
  official_name text NOT NULL,
  unit_type text NOT NULL,
  valid_during tstzrange NOT NULL,
  boundary geometry(MultiPolygon,4326),
  source_ref text NOT NULL,
  CHECK (NOT isempty(valid_during)),
  CHECK (boundary IS NULL OR ST_IsValid(boundary)),
  EXCLUDE USING gist
    (release_id WITH =, unit_id WITH =, valid_during WITH &&)
);

CREATE TABLE geo.hierarchy_edge (
  id uuid PRIMARY KEY,
  release_id uuid NOT NULL REFERENCES geo.release(id),
  child_unit_id uuid NOT NULL REFERENCES geo.admin_unit(id),
  parent_unit_id uuid NOT NULL REFERENCES geo.admin_unit(id),
  hierarchy_kind text NOT NULL,
  valid_during tstzrange NOT NULL,
  source_ref text NOT NULL,
  CHECK (child_unit_id <> parent_unit_id),
  CHECK (NOT isempty(valid_during)),
  EXCLUDE USING gist
    (release_id WITH =, child_unit_id WITH =,
     hierarchy_kind WITH =, valid_during WITH &&)
);

CREATE TABLE geo.unit_lineage (
  predecessor_id uuid NOT NULL REFERENCES geo.admin_unit(id),
  successor_id uuid NOT NULL REFERENCES geo.admin_unit(id),
  change_kind text NOT NULL CHECK (change_kind IN ('SPLIT','MERGE','REPLACED')),
  effective_at timestamptz NOT NULL,
  source_ref text NOT NULL,
  PRIMARY KEY (predecessor_id, successor_id, effective_at),
  CHECK (predecessor_id <> successor_id)
);

CREATE TABLE geo.responsibility_rule (
  id uuid PRIMARY KEY,
  release_id uuid NOT NULL REFERENCES geo.release(id),
  agency_id uuid NOT NULL REFERENCES ops.agency(id),
  category_code text NOT NULL,
  obligation_type text NOT NULL,
  coverage_unit_id uuid REFERENCES geo.admin_unit(id),
  service_boundary geometry(MultiPolygon,4326),
  asset_scope_ref text,
  valid_during tstzrange NOT NULL,
  precedence integer NOT NULL,
  authority_source_ref text NOT NULL,
  source_document_hash text NOT NULL,
  CHECK (NOT isempty(valid_during)),
  CHECK (coverage_unit_id IS NOT NULL OR service_boundary IS NOT NULL
         OR asset_scope_ref IS NOT NULL)
);

CREATE TABLE ops.route_decision (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  case_version bigint NOT NULL,
  release_id uuid NOT NULL REFERENCES geo.release(id),
  valid_at timestamptz NOT NULL,
  input_fingerprint text NOT NULL,
  candidate_rules jsonb NOT NULL,
  result text NOT NULL CHECK
    (result IN ('PROPOSED','AMBIGUOUS','NO_MATCH','MANUAL_REVIEW')),
  explanation jsonb NOT NULL,
  decided_at timestamptz NOT NULL DEFAULT now(),
  decided_by uuid,
  model_version text
);

ALTER TABLE social.community ADD CONSTRAINT community_admin_unit_fk
  FOREIGN KEY (admin_unit_id) REFERENCES geo.admin_unit(id);

CREATE INDEX unit_boundary_idx ON geo.unit_version USING gist (boundary);
CREATE INDEX rule_boundary_idx
  ON geo.responsibility_rule USING gist (service_boundary);


CREATE SCHEMA identity;

CREATE TABLE identity.principal (
  id uuid PRIMARY KEY,
  profile_id uuid UNIQUE REFERENCES social.profile(id),
  state text NOT NULL CHECK (state IN ('ACTIVE','SUSPENDED','CLOSED')),
  authorization_version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE identity.account_binding (
  provider text NOT NULL,
  provider_subject text NOT NULL,
  principal_id uuid NOT NULL REFERENCES identity.principal(id),
  state text NOT NULL CHECK (state IN ('ACTIVE','REVOKED')),
  PRIMARY KEY (provider, provider_subject)
);
CREATE TABLE identity.session (
  id uuid PRIMARY KEY,
  principal_id uuid NOT NULL REFERENCES identity.principal(id),
  token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at)
);
CREATE TABLE identity.organization_grant (
  id uuid PRIMARY KEY,
  principal_id uuid NOT NULL REFERENCES identity.principal(id),
  agency_id uuid NOT NULL REFERENCES ops.agency(id),
  role text NOT NULL CHECK (role IN ('AGENCY_AGENT','AGENCY_LEAD','FIELD_WORKER','VERIFIER')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz NOT NULL,
  revoked_at timestamptz,
  version bigint NOT NULL DEFAULT 1,
  CHECK (valid_to > valid_from)
);
CREATE INDEX organization_scope_idx
  ON identity.organization_grant (principal_id, agency_id, valid_to)
  WHERE revoked_at IS NULL;
CREATE TABLE identity.platform_grant (
  id uuid PRIMARY KEY,
  principal_id uuid NOT NULL REFERENCES identity.principal(id),
  role text NOT NULL CHECK (role IN
    ('COORDINATOR','ADJUDICATOR','PUBLISHER','PLATFORM_MODERATOR','APPEAL_REVIEWER')),
  area_id uuid REFERENCES geo.admin_unit(id),
  valid_to timestamptz NOT NULL,
  revoked_at timestamptz,
  version bigint NOT NULL DEFAULT 1
);

CREATE TABLE social.feed_preference (
  profile_id uuid PRIMARY KEY REFERENCES social.profile(id),
  locale text NOT NULL DEFAULT 'en-IN',
  interests text[] NOT NULL DEFAULT '{}',
  coarse_area_id uuid REFERENCES geo.admin_unit(id),
  personalization_enabled boolean NOT NULL DEFAULT false,
  theme text NOT NULL DEFAULT 'SYSTEM' CHECK (theme IN ('SYSTEM','LIGHT','DARK')),
  density text NOT NULL DEFAULT 'COMFORTABLE' CHECK (density IN ('COMFORTABLE','COMPACT')),
  notification_channels text[] NOT NULL DEFAULT ARRAY['IN_APP'],
  quiet_hours jsonb NOT NULL DEFAULT '{}',
  policy_version text NOT NULL,
  version bigint NOT NULL DEFAULT 1
);
CREATE TABLE social.mute (
  id uuid PRIMARY KEY,
  profile_id uuid NOT NULL REFERENCES social.profile(id),
  muted_profile_id uuid REFERENCES social.profile(id),
  muted_community_id uuid REFERENCES social.community(id),
  expires_at timestamptz,
  CHECK (num_nonnulls(muted_profile_id, muted_community_id) = 1)
);
CREATE UNIQUE INDEX mute_profile_unique ON social.mute(profile_id, muted_profile_id);
CREATE UNIQUE INDEX mute_community_unique ON social.mute(profile_id, muted_community_id);
CREATE TABLE social.post_stats (
  post_id uuid PRIMARY KEY REFERENCES social.post(id),
  up_count bigint NOT NULL DEFAULT 0 CHECK (up_count >= 0),
  down_count bigint NOT NULL DEFAULT 0 CHECK (down_count >= 0),
  comment_count bigint NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
  repost_count bigint NOT NULL DEFAULT 0 CHECK (repost_count >= 0),
  as_of timestamptz NOT NULL
);
CREATE TABLE social.comment_stats (
  comment_id uuid PRIMARY KEY REFERENCES social.comment(id),
  up_count bigint NOT NULL DEFAULT 0 CHECK (up_count >= 0),
  down_count bigint NOT NULL DEFAULT 0 CHECK (down_count >= 0),
  as_of timestamptz NOT NULL
);
CREATE TABLE social.selected_response (
  post_id uuid PRIMARY KEY REFERENCES social.post(id),
  comment_id uuid NOT NULL,
  selected_by uuid NOT NULL REFERENCES social.profile(id),
  selected_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (post_id, comment_id) REFERENCES social.comment(post_id, id)
);
CREATE TABLE social.post_case_link (
  post_id uuid NOT NULL REFERENCES social.post(id),
  receipt_id uuid NOT NULL REFERENCES social.case_receipt(id),
  PRIMARY KEY (post_id, receipt_id)
);
CREATE TABLE social.playbook_revision (
  post_id uuid NOT NULL,
  revision integer NOT NULL,
  steps jsonb NOT NULL CHECK (jsonb_typeof(steps) = 'array'),
  applicability text NOT NULL,
  reviewer_ref uuid,
  reviewed_at timestamptz,
  PRIMARY KEY (post_id, revision),
  FOREIGN KEY (post_id, revision) REFERENCES social.post_revision(post_id, revision)
);
CREATE TABLE social.moderation_case (
  id uuid PRIMARY KEY,
  post_id uuid REFERENCES social.post(id),
  comment_id uuid REFERENCES social.comment(id),
  media_id uuid REFERENCES social.media_asset(id),
  profile_id uuid REFERENCES social.profile(id),
  target_version bigint NOT NULL,
  reporter_ref uuid,
  reason_code text NOT NULL,
  grounds text NOT NULL,
  state text NOT NULL CHECK (state IN ('OPEN','REVIEWING','DECIDED','CLOSED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (num_nonnulls(post_id, comment_id, media_id, profile_id) = 1)
);
CREATE TABLE social.moderation_decision (
  id uuid PRIMARY KEY,
  moderation_case_id uuid NOT NULL REFERENCES social.moderation_case(id),
  sequence bigint NOT NULL,
  action text NOT NULL CHECK (action IN ('ALLOW','HIDE','REMOVE','RESTRICT','RESTORE')),
  rule_version text NOT NULL,
  actor_ref uuid NOT NULL,
  reason text NOT NULL,
  decided_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (moderation_case_id, sequence)
);
CREATE TABLE social.appeal (
  id uuid PRIMARY KEY,
  decision_id uuid NOT NULL REFERENCES social.moderation_decision(id),
  appellant_ref uuid NOT NULL,
  grounds text NOT NULL,
  state text NOT NULL CHECK (state IN ('OPEN','REVIEWING','UPHELD','REVERSED','WITHDRAWN')),
  reviewer_ref uuid,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (decision_id, appellant_ref)
);
CREATE TABLE social.notification (
  id uuid PRIMARY KEY,
  recipient_id uuid NOT NULL REFERENCES social.profile(id),
  event_id uuid NOT NULL,
  channel text NOT NULL CHECK (channel IN ('IN_APP','EMAIL','PUSH')),
  post_id uuid REFERENCES social.post(id),
  receipt_id uuid REFERENCES social.case_receipt(id),
  state text NOT NULL CHECK (state IN ('QUEUED','SENT','SUPPRESSED','FAILED')),
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz,
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (num_nonnulls(post_id, receipt_id) <= 1),
  UNIQUE (recipient_id, event_id, channel)
);
CREATE TABLE social.feed_snapshot (
  id uuid PRIMARY KEY,
  viewer_scope_hash bytea NOT NULL,
  query_hash bytea NOT NULL,
  ranking_version text NOT NULL,
  candidate_ids jsonb NOT NULL CHECK (jsonb_typeof(candidate_ids) = 'array'),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE social.community ADD COLUMN description text NOT NULL DEFAULT '';
ALTER TABLE social.community ADD COLUMN language_tag text NOT NULL DEFAULT 'und';
ALTER TABLE social.profile ADD COLUMN avatar_media_id uuid REFERENCES social.media_asset(id);
CREATE TABLE social.community_request (
  id uuid PRIMARY KEY,
  requested_by uuid NOT NULL REFERENCES social.profile(id),
  title text NOT NULL,
  proposed_slug citext NOT NULL,
  scope_kind text NOT NULL CHECK (scope_kind IN ('GEOGRAPHIC','TOPIC')),
  admin_unit_id uuid REFERENCES geo.admin_unit(id),
  language_tag text NOT NULL,
  steward_note text NOT NULL,
  state text NOT NULL CHECK (state IN ('RECEIVED','REVIEWING','APPROVED','REJECTED')),
  community_id uuid REFERENCES social.community(id),
  reason text,
  version bigint NOT NULL DEFAULT 1
);
CREATE TABLE social.membership_decision (
  id uuid PRIMARY KEY,
  community_id uuid NOT NULL,
  profile_id uuid NOT NULL,
  actor_ref uuid NOT NULL,
  action text NOT NULL CHECK (action IN ('APPROVE','BAN','UNBAN','CHANGE_ROLE')),
  reason text NOT NULL,
  expires_at timestamptz,
  decided_at timestamptz NOT NULL,
  FOREIGN KEY (community_id, profile_id)
    REFERENCES social.community_member(community_id, profile_id)
);
CREATE TABLE social.case_receipt_event (
  receipt_id uuid NOT NULL REFERENCES social.case_receipt(id),
  sequence bigint NOT NULL,
  type text NOT NULL,
  safe_text text NOT NULL,
  actor_type text NOT NULL CHECK (actor_type IN ('PLATFORM','AGENCY','REVIEWER')),
  occurred_at timestamptz NOT NULL,
  projection_version bigint NOT NULL,
  PRIMARY KEY (receipt_id, sequence)
);
CREATE TABLE social.search_document (
  id uuid PRIMARY KEY,
  post_id uuid REFERENCES social.post(id),
  receipt_id uuid REFERENCES social.case_receipt(id),
  community_id uuid REFERENCES social.community(id),
  profile_id uuid REFERENCES social.profile(id),
  source_version bigint NOT NULL,
  language_tag text NOT NULL,
  safe_text text NOT NULL,
  category_code text,
  area_id uuid REFERENCES geo.admin_unit(id),
  updated_at timestamptz NOT NULL,
  CHECK (num_nonnulls(post_id, receipt_id, community_id, profile_id) = 1)
);
CREATE INDEX search_text_trigram_idx
  ON social.search_document USING gin (lower(safe_text) gin_trgm_ops);
CREATE UNIQUE INDEX search_post_language_unique ON social.search_document(post_id, language_tag);
CREATE UNIQUE INDEX search_receipt_language_unique ON social.search_document(receipt_id, language_tag);
CREATE UNIQUE INDEX search_community_language_unique ON social.search_document(community_id, language_tag);
CREATE UNIQUE INDEX search_profile_language_unique ON social.search_document(profile_id, language_tag);

CREATE TABLE infra.upload_session (
  id uuid PRIMARY KEY,
  media_id uuid NOT NULL UNIQUE REFERENCES social.media_asset(id),
  storage_upload_ref text NOT NULL,
  part_size_bytes bigint NOT NULL CHECK (part_size_bytes > 0),
  declared_size_bytes bigint NOT NULL CHECK (declared_size_bytes > 0),
  state text NOT NULL CHECK (state IN ('OPEN','COMPLETING','COMPLETE','ABORTED','EXPIRED')),
  expires_at timestamptz NOT NULL,
  version bigint NOT NULL DEFAULT 1
);
CREATE TABLE infra.media_derivative (
  id uuid PRIMARY KEY,
  media_id uuid NOT NULL REFERENCES social.media_asset(id),
  source_sha256 bytea NOT NULL CHECK (octet_length(source_sha256) = 32),
  storage_key text NOT NULL UNIQUE,
  transform_version text NOT NULL,
  pixel_width integer NOT NULL CHECK (pixel_width > 0),
  pixel_height integer NOT NULL CHECK (pixel_height > 0),
  original_to_derivative jsonb NOT NULL,
  approved_at timestamptz,
  revoked_at timestamptz
);
CREATE TABLE infra.language_capability (
  language_tag text NOT NULL,
  capability text NOT NULL CHECK (capability IN
    ('UI','SEARCH','VOICE_TRANSCRIPTION','OCR','CONTENT_TRANSLATION')),
  status text NOT NULL CHECK (status IN ('PLANNED','EVALUATING','READY','PAUSED')),
  pack_version text NOT NULL,
  evidence_ref text,
  reviewer_ref uuid,
  fallback_language_tag text,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (language_tag, capability),
  CHECK (status <> 'READY' OR (evidence_ref IS NOT NULL AND reviewer_ref IS NOT NULL))
);
CREATE TABLE infra.analysis_job (
  id uuid PRIMARY KEY,
  media_id uuid NOT NULL REFERENCES social.media_asset(id),
  requested_by uuid REFERENCES identity.principal(id),
  report_alias_ref uuid,
  source_sha256 bytea NOT NULL CHECK (octet_length(source_sha256) = 32),
  authorization_version bigint NOT NULL,
  language_tag text NOT NULL,
  state text NOT NULL CHECK (state IN
    ('QUEUED','RUNNING','SUCCEEDED','PARTIAL','FAILED','CANCELLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  CHECK (num_nonnulls(requested_by, report_alias_ref) = 1)
);
CREATE TABLE infra.analysis_task (
  id uuid PRIMARY KEY,
  job_id uuid NOT NULL REFERENCES infra.analysis_job(id),
  task_kind text NOT NULL CHECK (task_kind IN
    ('QUALITY','OCR','ISSUE_DETECTION','REDACTION','VOICE_TRANSCRIPTION')),
  state text NOT NULL CHECK (state IN
    ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNSUPPORTED','CANCELLED')),
  attempt integer NOT NULL DEFAULT 0,
  model_version text,
  result_schema_version integer NOT NULL,
  result jsonb,
  error_code text,
  completed_at timestamptz,
  UNIQUE (job_id, task_kind)
);

ALTER TABLE ops.report ADD COLUMN request_hash bytea NOT NULL
  CHECK (octet_length(request_hash) = 32);
ALTER TABLE ops.obligation ADD COLUMN required_for_restoration boolean NOT NULL DEFAULT true;
ALTER TABLE ops.obligation ADD CONSTRAINT obligation_case_id_unique UNIQUE (case_id, id);
ALTER TABLE ops.case_event ADD CONSTRAINT case_event_case_id_unique UNIQUE (case_id, id);
CREATE TABLE ops.coordinator_assignment (
  case_id uuid PRIMARY KEY REFERENCES ops.case_record(id),
  principal_id uuid NOT NULL REFERENCES identity.principal(id),
  roster_version text NOT NULL,
  assigned_at timestamptz NOT NULL,
  version bigint NOT NULL DEFAULT 1
);
CREATE TABLE ops.dispute (
  id uuid PRIMARY KEY,
  obligation_id uuid NOT NULL REFERENCES ops.obligation(id),
  reason_code text NOT NULL CHECK (reason_code IN
    ('JURISDICTION','ASSET_OWNER','MANDATE','DUPLICATE','INSUFFICIENT_EVIDENCE')),
  source_refs jsonb NOT NULL CHECK (jsonb_array_length(source_refs) > 0),
  proposed_destination_id uuid REFERENCES ops.agency(id),
  coordinator_ref uuid NOT NULL REFERENCES identity.principal(id),
  state text NOT NULL CHECK (state IN ('OPEN','EVIDENCE_PENDING','ADJUDICATING','DECIDED')),
  decision jsonb,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE ops.clock_policy (
  id text PRIMARY KEY,
  version text NOT NULL,
  clock_type text NOT NULL,
  calendar jsonb NOT NULL,
  duration_seconds bigint NOT NULL CHECK (duration_seconds > 0),
  signed_source_ref text NOT NULL,
  UNIQUE (id, version)
);
CREATE TABLE ops.deadline (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  obligation_id uuid,
  start_event_id uuid NOT NULL,
  clock_type text NOT NULL,
  policy_id text NOT NULL,
  policy_version text NOT NULL,
  started_at timestamptz NOT NULL,
  due_at timestamptz NOT NULL,
  breached_at timestamptz,
  stopped_at timestamptz,
  FOREIGN KEY (case_id, obligation_id) REFERENCES ops.obligation(case_id, id),
  FOREIGN KEY (case_id, start_event_id) REFERENCES ops.case_event(case_id, id),
  FOREIGN KEY (policy_id, policy_version) REFERENCES ops.clock_policy(id, version),
  CHECK (due_at >= started_at)
);
CREATE INDEX deadline_due_idx ON ops.deadline(due_at, id)
  WHERE breached_at IS NULL AND stopped_at IS NULL;
CREATE TABLE ops.agency_delivery (
  id uuid PRIMARY KEY,
  obligation_id uuid NOT NULL REFERENCES ops.obligation(id),
  destination_ref text NOT NULL,
  external_id text,
  delivery_key uuid NOT NULL UNIQUE,
  state text NOT NULL CHECK (state IN
    ('QUEUED','SENDING','DELIVERED','AMBIGUOUS','FAILED','CANCELLED')),
  delivered_at timestamptz,
  acknowledged_at timestamptz,
  attempts integer NOT NULL DEFAULT 0
);
CREATE TABLE ops.verification_decision (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  obligation_id uuid NOT NULL,
  reviewer_ref uuid NOT NULL REFERENCES identity.principal(id),
  result text NOT NULL CHECK (result IN ('VERIFIED','NOT_RESTORED','INSUFFICIENT')),
  reason text NOT NULL,
  decided_at timestamptz NOT NULL,
  FOREIGN KEY (case_id, obligation_id) REFERENCES ops.obligation(case_id, id)
);
CREATE TABLE ops.verification_evidence (
  decision_id uuid NOT NULL REFERENCES ops.verification_decision(id),
  evidence_id uuid NOT NULL REFERENCES ops.evidence(id),
  PRIMARY KEY (decision_id, evidence_id)
);
CREATE TABLE ops.publication_decision (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  case_version bigint NOT NULL,
  action text NOT NULL CHECK (action IN ('PUBLISH','CORRECT','WITHDRAW')),
  safe_payload jsonb,
  reviewer_ref uuid NOT NULL REFERENCES identity.principal(id),
  policy_version text NOT NULL,
  decided_at timestamptz NOT NULL,
  CHECK (action = 'WITHDRAW' OR safe_payload IS NOT NULL)
);
CREATE TABLE ops.integration_inbox (
  partner text NOT NULL,
  event_id text NOT NULL,
  payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
  external_id text NOT NULL,
  state text NOT NULL CHECK (state IN ('RECEIVED','APPLIED','REVIEW_REQUIRED','REJECTED')),
  received_at timestamptz NOT NULL,
  PRIMARY KEY (partner, event_id)
);
CREATE TABLE ops.export_job (
  id uuid PRIMARY KEY,
  requested_by uuid NOT NULL REFERENCES identity.principal(id),
  agency_id uuid REFERENCES ops.agency(id),
  approved_fields text[] NOT NULL,
  filters jsonb NOT NULL,
  purpose_code text NOT NULL,
  authorization_version bigint NOT NULL,
  state text NOT NULL CHECK (state IN ('QUEUED','RUNNING','READY','REVOKED','FAILED','EXPIRED')),
  storage_key text,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE TABLE infra.projection_checkpoint (
  consumer_name text NOT NULL,
  aggregate_id uuid NOT NULL,
  applied_version bigint NOT NULL,
  PRIMARY KEY (consumer_name, aggregate_id)
);
CREATE TABLE infra.deletion_task (
  id uuid PRIMARY KEY,
  object_kind text NOT NULL,
  object_id uuid NOT NULL,
  store text NOT NULL,
  policy_id text NOT NULL,
  action text NOT NULL,
  state text NOT NULL CHECK (state IN ('QUEUED','RUNNING','COMPLETE','HELD','FAILED')),
  completed_at timestamptz
);
CREATE TABLE infra.audit_event (
  id uuid PRIMARY KEY,
  actor_ref uuid NOT NULL,
  purpose_code text NOT NULL,
  object_kind text NOT NULL,
  object_id uuid NOT NULL,
  action text NOT NULL,
  result_code text NOT NULL,
  trace_id text NOT NULL,
  occurred_at timestamptz NOT NULL
);


CREATE TABLE ops.task (
  id uuid PRIMARY KEY,
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  deadline_id uuid REFERENCES ops.deadline(id),
  type text NOT NULL,
  state text NOT NULL CHECK (state IN ('OPEN','ASSIGNED','DONE','CANCELLED')),
  assigned_to uuid REFERENCES identity.principal(id),
  action_key text NOT NULL UNIQUE,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL,
  due_at timestamptz
);
CREATE INDEX task_queue_idx ON ops.task(assigned_to, state, due_at, id);
CREATE TABLE ops.completion_evidence (
  obligation_id uuid NOT NULL REFERENCES ops.obligation(id),
  evidence_id uuid NOT NULL REFERENCES ops.evidence(id),
  claimed_at timestamptz NOT NULL,
  PRIMARY KEY (obligation_id, evidence_id)
);
ALTER TABLE ops.obligation ADD COLUMN parent_obligation_id uuid REFERENCES ops.obligation(id);
CREATE TABLE ops.information_request (
  id uuid PRIMARY KEY,
  report_id uuid NOT NULL REFERENCES ops.report(id),
  case_id uuid NOT NULL REFERENCES ops.case_record(id),
  requested_by uuid NOT NULL REFERENCES identity.principal(id),
  prompt text NOT NULL,
  state text NOT NULL CHECK (state IN ('OPEN','ANSWERED','CANCELLED')),
  response_text text,
  created_at timestamptz NOT NULL,
  answered_at timestamptz,
  version bigint NOT NULL DEFAULT 1
);

-- +goose StatementEnd

-- +goose Down
SELECT 1;
