"""Append-only persistence for property_scores (spec §13.2 point 4) — this
sidecar role is the one thing granted INSERT here (spec §13.3.1), and never
UPDATE. A duplicate (property_id, as_of_date, model_version) is treated as
already-computed and its existing row is returned, never overwritten.
"""

import json

_INSERT = """
    INSERT INTO property_scores (
        organization_id, property_id, as_of_date, model_version, noi, dscr,
        score_value, score_band, feature_snapshot, requested_by
    ) VALUES (%(organization_id)s, %(property_id)s, %(as_of_date)s, %(model_version)s,
              %(noi)s, %(dscr)s, %(score_value)s, %(score_band)s, %(feature_snapshot)s, %(requested_by)s)
    ON CONFLICT (property_id, as_of_date, model_version) DO NOTHING
    RETURNING id, organization_id, property_id, computed_at, as_of_date, model_version,
              noi, dscr, score_value, score_band, requested_by
"""

_SELECT_EXISTING = """
    SELECT id, organization_id, property_id, computed_at, as_of_date, model_version,
           noi, dscr, score_value, score_band, requested_by
    FROM property_scores
    WHERE property_id = %(property_id)s AND as_of_date = %(as_of_date)s AND model_version = %(model_version)s
"""

_COLUMNS = [
    "id", "organization_id", "property_id", "computed_at", "as_of_date", "model_version",
    "noi", "dscr", "score_value", "score_band", "requested_by",
]


def persist_score(conn, **fields) -> dict:
    params = dict(fields)
    params["feature_snapshot"] = json.dumps(params["feature_snapshot"])

    with conn.cursor() as cur:
        cur.execute(_INSERT, params)
        row = cur.fetchone()
        if row is None:
            cur.execute(
                _SELECT_EXISTING,
                {"property_id": params["property_id"], "as_of_date": params["as_of_date"], "model_version": params["model_version"]},
            )
            row = cur.fetchone()
        conn.commit()
        return dict(zip(_COLUMNS, row))
