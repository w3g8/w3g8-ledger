-- Add funding intent integration columns to card_payments
ALTER TABLE card_payments
    ADD COLUMN IF NOT EXISTS wallet_id VARCHAR(26),
    ADD COLUMN IF NOT EXISTS intent_id VARCHAR(26) REFERENCES funding_intents(id),
    ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS chargeback_at TIMESTAMPTZ;

-- Add indexes for new columns
CREATE INDEX IF NOT EXISTS idx_card_payments_intent ON card_payments(intent_id);
CREATE INDEX IF NOT EXISTS idx_card_payments_wallet ON card_payments(wallet_id);

-- Add recall/return columns to FPS payments
ALTER TABLE fps_payments
    ADD COLUMN IF NOT EXISTS amount_minor BIGINT,
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3),
    ADD COLUMN IF NOT EXISTS recalled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recall_reason VARCHAR(10),
    ADD COLUMN IF NOT EXISTS recall_ref VARCHAR(255),
    ADD COLUMN IF NOT EXISTS returned_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS return_reason VARCHAR(50);

CREATE INDEX IF NOT EXISTS idx_fps_payments_status ON fps_payments(fps_status) WHERE fps_status IN ('RECALLED', 'RETURNED');

-- Add recall/return columns to SEPA payments
ALTER TABLE sepa_payments
    ADD COLUMN IF NOT EXISTS amount_minor BIGINT,
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3),
    ADD COLUMN IF NOT EXISTS recalled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recall_reason VARCHAR(10),
    ADD COLUMN IF NOT EXISTS recall_ref VARCHAR(255),
    ADD COLUMN IF NOT EXISTS recall_additional_info TEXT,
    ADD COLUMN IF NOT EXISTS returned_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS return_reason VARCHAR(50);

CREATE INDEX IF NOT EXISTS idx_sepa_payments_status ON sepa_payments(sepa_status) WHERE sepa_status IN ('RECALLED', 'RETURNED');

-- Chargebacks table for card payments
CREATE TABLE IF NOT EXISTS card_chargebacks (
    id VARCHAR(26) PRIMARY KEY,
    card_payment_id VARCHAR(26) NOT NULL REFERENCES card_payments(id),
    chargeback_id VARCHAR(255) NOT NULL UNIQUE,
    intent_id VARCHAR(26) REFERENCES funding_intents(id),
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    reason VARCHAR(255),
    reason_code VARCHAR(20),
    stage VARCHAR(20) NOT NULL DEFAULT 'first_chargeback', -- first_chargeback, representment, pre_arbitration, arbitration
    status VARCHAR(20) NOT NULL DEFAULT 'open', -- open, disputed, accepted, won, lost
    network_ref VARCHAR(255),
    acquirer_ref VARCHAR(255),
    response_due_date TIMESTAMPTZ,
    responded_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    evidence_submitted JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_chargebacks_payment ON card_chargebacks(card_payment_id);
CREATE INDEX idx_chargebacks_intent ON card_chargebacks(intent_id);
CREATE INDEX idx_chargebacks_status ON card_chargebacks(status) WHERE status = 'open';
CREATE INDEX idx_chargebacks_due_date ON card_chargebacks(response_due_date) WHERE status = 'open';
