-- 0003_device_model.sql: remember which kind of device wrote a row.
--
-- Apple's export has a `device` attribute such as
--   <<HKDevice: 0x...>, name:Apple Watch, manufacturer:Apple Inc., model:Watch, ...>
-- The importer keeps its `model:` part (Watch, iPhone, ...) here. The read
-- side uses it to tell a Watch from an iPhone, because source NAMES are free
-- text that the user can rename. '' means unknown (rows imported before this
-- migration, or exports without the attribute); readers then fall back to the
-- name. ADD COLUMN with a constant default needs no table rebuild and does not
-- touch the existing rows, so this file has no rebuild marker.
--
-- The column is NOT part of any UNIQUE constraint: a row is still the same row
-- whatever its device model, so re-imports keep deduplicating as before.
ALTER TABLE samples      ADD COLUMN device_model TEXT NOT NULL DEFAULT '';
ALTER TABLE sleep_stages ADD COLUMN device_model TEXT NOT NULL DEFAULT '';
ALTER TABLE workouts     ADD COLUMN device_model TEXT NOT NULL DEFAULT '';
