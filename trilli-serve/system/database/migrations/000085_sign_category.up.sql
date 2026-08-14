-- Trilli Sign: envelope categorization (shown in the dashboard index).
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
