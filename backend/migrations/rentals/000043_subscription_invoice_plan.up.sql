-- A renewal can now choose a different plan than the manager's current one
-- (system-design.txt 3.6): the invoice must remember which plan was chosen
-- so the webhook activates that plan, not whatever the subscription was on
-- when the push went out. Backfill existing invoices from their subscription
-- at the time, since that is the only plan they could have chosen.
ALTER TABLE subscription_invoices ADD COLUMN plan_id uuid REFERENCES subscription_plans (id);

UPDATE subscription_invoices si
SET plan_id = s.plan_id
FROM subscriptions s
WHERE s.id = si.subscription_id AND si.plan_id IS NULL;

ALTER TABLE subscription_invoices ALTER COLUMN plan_id SET NOT NULL;
