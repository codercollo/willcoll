-- property_scores (spec §3.5, §13) — append-only. One new row per computation;
-- NEVER updated (a re-run produces a NEW row with a new as_of_date). SELECT and
-- INSERT policies only, mirroring ledger_entries' immutability.

CREATE TABLE property_scores (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE CASCADE,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    as_of_date DATE NOT NULL,            -- the ledger cutoff the score was computed against
    model_version TEXT NOT NULL,         -- matches ml-sidecar/ml/model_card.md, §13.1
    noi NUMERIC(14,2) NOT NULL,          -- Net Operating Income over the scoring window
    dscr NUMERIC(6,3) NOT NULL,          -- Debt Service Coverage Ratio
    score_value NUMERIC(6,2) NOT NULL,   -- raw model output
    score_band TEXT NOT NULL,            -- 'A' | 'B' | 'C' | 'D', §13.2
    feature_snapshot JSONB NOT NULL,     -- exact feature vector fed to the model
    requested_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT, -- always a Manager, §13.4
    UNIQUE (property_id, as_of_date, model_version)
);

-- Append-only: only SELECT and INSERT policies exist.
ALTER TABLE property_scores ENABLE ROW LEVEL SECURITY;
CREATE POLICY property_scores_select ON property_scores FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY property_scores_insert ON property_scores FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
