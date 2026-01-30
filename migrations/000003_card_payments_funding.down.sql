-- Drop chargebacks table
DROP TABLE IF EXISTS card_chargebacks;

-- Remove recall/return columns from SEPA payments
ALTER TABLE sepa_payments
    DROP COLUMN IF EXISTS amount_minor,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS recalled_at,
    DROP COLUMN IF EXISTS recall_reason,
    DROP COLUMN IF EXISTS recall_ref,
    DROP COLUMN IF EXISTS recall_additional_info,
    DROP COLUMN IF EXISTS returned_at,
    DROP COLUMN IF EXISTS return_reason;

DROP INDEX IF EXISTS idx_sepa_payments_status;

-- Remove recall/return columns from FPS payments
ALTER TABLE fps_payments
    DROP COLUMN IF EXISTS amount_minor,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS recalled_at,
    DROP COLUMN IF EXISTS recall_reason,
    DROP COLUMN IF EXISTS recall_ref,
    DROP COLUMN IF EXISTS returned_at,
    DROP COLUMN IF EXISTS return_reason;

DROP INDEX IF EXISTS idx_fps_payments_status;

-- Remove funding intent integration columns from card_payments
DROP INDEX IF EXISTS idx_card_payments_wallet;
DROP INDEX IF EXISTS idx_card_payments_intent;

ALTER TABLE card_payments
    DROP COLUMN IF EXISTS wallet_id,
    DROP COLUMN IF EXISTS intent_id,
    DROP COLUMN IF EXISTS refunded_at,
    DROP COLUMN IF EXISTS chargeback_at;
