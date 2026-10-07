-- +goose Up
CREATE SCHEMA rec_serving;
REVOKE ALL ON SCHEMA rec_serving FROM PUBLIC;
CREATE TABLE rec_serving.control (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 disabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO rec_serving.control(singleton) VALUES(true);
CREATE TABLE rec_serving.control_audit (
 version bigint PRIMARY KEY,
 disabled boolean NOT NULL,
 changed_at timestamptz NOT NULL DEFAULT now(),
 database_actor text NOT NULL
);
-- Neither serving nor operator logins can read/write the tables directly.
-- +goose StatementBegin
CREATE FUNCTION rec_serving.current_control() RETURNS TABLE(disabled boolean,version bigint)
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT c.disabled,c.version FROM rec_serving.control c WHERE c.singleton
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION rec_serving.set_disabled(p_disabled boolean,p_version bigint)
 RETURNS TABLE(disabled boolean,version bigint)
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE old_disabled boolean; old_version bigint;
BEGIN
 IF p_disabled IS NULL OR p_version IS NULL OR p_version<1 THEN
  RAISE EXCEPTION 'Invalid serving control' USING ERRCODE='22023';
 END IF;
 SELECT c.disabled,c.version INTO old_disabled,old_version FROM rec_serving.control c WHERE c.singleton FOR UPDATE;
 IF old_version IS DISTINCT FROM p_version THEN RETURN; END IF;
 IF old_disabled IS DISTINCT FROM p_disabled THEN
  UPDATE rec_serving.control c SET disabled=p_disabled,version=c.version+1,updated_at=statement_timestamp() WHERE c.singleton;
  INSERT INTO rec_serving.control_audit(version,disabled,database_actor) VALUES(old_version+1,p_disabled,session_user);
 END IF;
 RETURN QUERY SELECT c.disabled,c.version FROM rec_serving.control c WHERE c.singleton;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION rec_serving.current_control(),rec_serving.set_disabled(boolean,bigint) FROM PUBLIC;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Serving control rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd
