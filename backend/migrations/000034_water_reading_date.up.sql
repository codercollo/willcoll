-- The date the meter was actually read, distinct from created_at (the row's
-- posting time): a manager can enter last week's reading today, and the
-- water bill must print the reading date, not whenever it happened to be
-- typed in. Defaults to CURRENT_DATE so existing rows stay sane.
ALTER TABLE water_readings ADD COLUMN IF NOT EXISTS reading_date date NOT NULL DEFAULT CURRENT_DATE;
