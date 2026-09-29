-- Business gauges for GET /debug/vars (system-design.txt section 5). Counts
-- only, across every manager, on the willcoll_admin pool (BYPASSRLS, SELECT
-- only); no row, id or amount leaves the database.

-- AdminReviewQueueDepth is how many payments still need a manager: those no
-- unit could be found for (the number that should trend to zero as intent
-- payments replace paybill ones), and those the engine placed on a guess.
-- name: AdminReviewQueueDepth :one
SELECT
    count(*) FILTER (WHERE status = 'unmatched')::bigint AS unmatched,
    count(*) FILTER (WHERE auto_applied_unconfirmed)::bigint AS unconfirmed
FROM payments;
