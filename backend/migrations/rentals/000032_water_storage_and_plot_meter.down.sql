DROP TABLE IF EXISTS property_meter_readings;
ALTER TABLE properties DROP COLUMN IF EXISTS rooftop_capacity_units;
ALTER TABLE properties DROP COLUMN IF EXISTS underground_capacity_units;
