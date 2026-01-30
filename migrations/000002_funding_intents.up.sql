-- Funding intents table
CREATE TABLE IF NOT EXISTS funding_intents (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL,
    wallet_id VARCHAR(26) NOT NULL,
    customer_id VARCHAR(26) NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    method VARCHAR(20) NOT NULL, -- OPEN_BANKING, SEPA, FPS, CARD, ACH
    status VARCHAR(20) NOT NULL DEFAULT 'created', -- created, pending, settled, failed, expired, reversed
    idempotency_key VARCHAR(255) NOT NULL,

    -- Provider fields
    provider_ref VARCHAR(255),
    redirect_url TEXT,
    bank_details JSONB,
    payment_session VARCHAR(255),

    -- Tracking
    attempt_count INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    reversed_at TIMESTAMPTZ,
    reversal_reason VARCHAR(255),

    -- Ledger reference
    ledger_batch_id VARCHAR(26),

    -- Metadata and errors
    metadata JSONB DEFAULT '{}',
    error_code VARCHAR(50),
    error_message TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,

    -- Constraints
    UNIQUE (tenant_id, idempotency_key)
);

-- Indexes
CREATE INDEX idx_funding_intents_tenant_status ON funding_intents(tenant_id, status);
CREATE INDEX idx_funding_intents_wallet ON funding_intents(wallet_id);
CREATE INDEX idx_funding_intents_customer ON funding_intents(customer_id);
CREATE INDEX idx_funding_intents_reference ON funding_intents USING GIN (bank_details);
CREATE INDEX idx_funding_intents_created ON funding_intents(created_at);

-- Funding attempts table
CREATE TABLE IF NOT EXISTS funding_attempts (
    id VARCHAR(26) PRIMARY KEY,
    intent_id VARCHAR(26) NOT NULL REFERENCES funding_intents(id),
    provider VARCHAR(50) NOT NULL,
    provider_ref VARCHAR(255),
    status VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending, submitted, settled, failed
    attempt_number INT NOT NULL DEFAULT 1,
    error_code VARCHAR(50),
    error_message TEXT,
    provider_data JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    submitted_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ
);

CREATE INDEX idx_funding_attempts_intent ON funding_attempts(intent_id);
CREATE INDEX idx_funding_attempts_provider_ref ON funding_attempts(provider_ref);

-- Open Banking payments table
CREATE TABLE IF NOT EXISTS openbanking_payments (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL,
    customer_id VARCHAR(26) NOT NULL,
    payment_id VARCHAR(255) NOT NULL,
    consent_id VARCHAR(255),
    scheme VARCHAR(20) NOT NULL, -- UK, EU_SEPA, EU_INSTANT
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    debtor_iban VARCHAR(50),
    debtor_name VARCHAR(255),
    reference VARCHAR(255),
    ob_status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    deposit_id VARCHAR(26),
    initiated_at TIMESTAMPTZ NOT NULL,
    authorised_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_code VARCHAR(50),
    error_message TEXT,
    response_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ob_payments_payment_id ON openbanking_payments(payment_id);
CREATE INDEX idx_ob_payments_tenant ON openbanking_payments(tenant_id);

-- Card payments table
CREATE TABLE IF NOT EXISTS card_payments (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL,
    customer_id VARCHAR(26) NOT NULL,
    card_token VARCHAR(255) NOT NULL,
    transaction_id VARCHAR(255),
    auth_code VARCHAR(50),
    card_last_four VARCHAR(4),
    card_brand VARCHAR(20),
    card_type VARCHAR(20),
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    three_ds_version VARCHAR(10),
    three_ds_status VARCHAR(20),
    card_status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    deposit_id VARCHAR(26),
    initiated_at TIMESTAMPTZ NOT NULL,
    authorised_at TIMESTAMPTZ,
    captured_at TIMESTAMPTZ,
    error_code VARCHAR(50),
    error_message TEXT,
    decline_reason VARCHAR(255),
    response_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_card_payments_txn ON card_payments(transaction_id);
CREATE INDEX idx_card_payments_tenant ON card_payments(tenant_id);

-- FPS payments table
CREATE TABLE IF NOT EXISTS fps_payments (
    id VARCHAR(26) PRIMARY KEY,
    payment_attempt_id VARCHAR(26),
    end_to_end_id VARCHAR(50) NOT NULL UNIQUE,
    provider_payment_id VARCHAR(255),
    sort_code VARCHAR(6),
    account_number VARCHAR(8),
    fps_status VARCHAR(20) NOT NULL DEFAULT 'SUBMITTED',
    submitted_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    error_code VARCHAR(50),
    error_message TEXT,
    response_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fps_payments_e2e ON fps_payments(end_to_end_id);

-- SEPA payments table
CREATE TABLE IF NOT EXISTS sepa_payments (
    id VARCHAR(26) PRIMARY KEY,
    payment_attempt_id VARCHAR(26),
    msg_id VARCHAR(50) NOT NULL,
    pmt_inf_id VARCHAR(50) NOT NULL,
    end_to_end_id VARCHAR(50) NOT NULL,
    iban VARCHAR(50),
    bic VARCHAR(11),
    creditor_name VARCHAR(255),
    sepa_status VARCHAR(20) NOT NULL DEFAULT 'SUBMITTED',
    submitted_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    reject_reason_code VARCHAR(10),
    reject_reason_desc TEXT,
    last_report_id VARCHAR(50),
    last_report_at TIMESTAMPTZ,
    response_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(msg_id, pmt_inf_id)
);

CREATE INDEX idx_sepa_payments_e2e ON sepa_payments(end_to_end_id);
