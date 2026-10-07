-- Period confirmation gate. The checksum is a hash of the confirmed totals;
-- staleness is detected by recomputing it on every PDF request and
-- comparing, not by a trigger (system-design.txt PDF confirmation addendum).

-- name: GetReportConfirmation :one
SELECT id, totals_checksum, note_lines, confirmed_by, confirmed_at
FROM report_confirmations
WHERE tenant_id = @tenant_id AND property_id = @property_id AND period = @period;

-- name: ArchiveReportConfirmation :exec
-- Copies the current confirmation row (if any) into history before a
-- re-confirm overwrites it. A no-op insert-select when nothing exists yet.
INSERT INTO report_confirmation_history (tenant_id, property_id, period, totals_checksum, note_lines, confirmed_by, confirmed_at)
SELECT rc.tenant_id, rc.property_id, rc.period, rc.totals_checksum, rc.note_lines, rc.confirmed_by, rc.confirmed_at
FROM report_confirmations rc
WHERE rc.tenant_id = @tenant_id AND rc.property_id = @property_id AND rc.period = @period;

-- name: UpsertReportConfirmation :one
INSERT INTO report_confirmations (tenant_id, property_id, period, totals_checksum, note_lines, confirmed_by)
VALUES (@tenant_id, @property_id, @period, @totals_checksum, @note_lines, @confirmed_by)
ON CONFLICT (property_id, period) DO UPDATE
SET totals_checksum = EXCLUDED.totals_checksum,
    note_lines = EXCLUDED.note_lines,
    confirmed_by = EXCLUDED.confirmed_by,
    confirmed_at = now()
RETURNING id, confirmed_at;
