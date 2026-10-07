-- Full-text search on tenant names, for the Units tab search box
-- (Greenlight ch.9.5; system-design.txt 6.1): matches a lease's tenant_name
-- or any lease_payers.name. Expression GIN indexes, so the search in
-- ListUnitsForProperty stays an index lookup as a firm's leases grow. The
-- expressions must match the query's to_tsvector calls exactly.
CREATE INDEX IF NOT EXISTS leases_tenant_name_fts_idx
    ON leases USING gin (to_tsvector('simple', tenant_name));

CREATE INDEX IF NOT EXISTS lease_payers_name_fts_idx
    ON lease_payers USING gin (to_tsvector('simple', name));
