-- C3.4: a background job writes audit rows as an explicit system actor.
-- actor_id is a uuid (a principal's id), which a job does not have, so the
-- job's name goes in actor_name. A system row must name its job; any other
-- actor must not carry one.
ALTER TABLE audit_log ADD COLUMN actor_name text NULL;

ALTER TABLE audit_log ADD CONSTRAINT audit_log_system_actor_named
    CHECK ((actor_type = 'system') = (actor_name IS NOT NULL));
