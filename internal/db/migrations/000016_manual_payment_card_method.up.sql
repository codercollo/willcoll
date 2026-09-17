-- Manual Payment Recording, Phase 2: AllocateManualPayment needs a
-- payment_method value for a manually-recorded card payment (e.g. a
-- physical POS terminal swipe entered after the fact). The existing
-- 'intasend_card' value means something different — a gateway-collected
-- card checkout (spec §5.5) — and reusing it here would misrepresent how
-- the money actually arrived.
ALTER TYPE payment_method ADD VALUE IF NOT EXISTS 'card_manual';
