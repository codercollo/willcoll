-- The plot meter was a single "units consumed" number typed in by hand;
-- store it the same way as a unit's water_readings instead — previous +
-- current dial readings, with units used derived, not typed — so a manager
-- reads the dial once and the math (and the "reject a lower current
-- reading" guard) is the same as every other meter in the system.
ALTER TABLE property_meter_readings RENAME COLUMN units_consumed TO current_reading;
ALTER TABLE property_meter_readings ADD COLUMN previous_reading numeric(12,2) NULL;
ALTER TABLE property_meter_readings ADD COLUMN reading_date date NOT NULL DEFAULT CURRENT_DATE;

-- Existing rows (typed as a raw consumption figure, not a dial reading)
-- become a current reading with no previous: there is no real prior dial
-- value to infer, so the first month after this migration is manually
-- editable, same as a brand-new unit meter.
ALTER TABLE property_meter_readings ADD COLUMN units_consumed numeric(12,2)
    GENERATED ALWAYS AS (current_reading - previous_reading) STORED;
