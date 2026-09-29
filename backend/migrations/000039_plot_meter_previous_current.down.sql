ALTER TABLE property_meter_readings DROP COLUMN units_consumed;
ALTER TABLE property_meter_readings DROP COLUMN reading_date;
ALTER TABLE property_meter_readings DROP COLUMN previous_reading;
ALTER TABLE property_meter_readings RENAME COLUMN current_reading TO units_consumed;
