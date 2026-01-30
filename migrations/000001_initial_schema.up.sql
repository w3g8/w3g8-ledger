-- Financial Platform Initial Schema
-- All tables include tenant isolation where applicable

-- ============================================================================
-- IAM Service Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS tenants (
    id VARCHAR(26) PRIMARY KEY,  -- ULID
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(100) NOT NULL UNIQUE,
    status VARCHAR(20) NOT NULL DEFAULT 'active',  -- active, suspended, deleted
    settings JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenants_slug ON tenants(slug);
CREATE INDEX idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(26) PRIMARY KEY,  -- ULID
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255),
    name VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',  -- active, suspended, deleted
    email_verified_at TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, email)
);

CREATE INDEX idx_users_tenant_id ON users(tenant_id);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_status ON users(status);

CREATE TABLE IF NOT EXISTS user_roles (
    id VARCHAR(26) PRIMARY KEY,
    user_id VARCHAR(26) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL,  -- admin, operator, viewer, etc.
    resource_type VARCHAR(50),  -- Optional: scope to resource type
    resource_id VARCHAR(26),    -- Optional: scope to specific resource
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, role, resource_type, resource_id)
);

CREATE INDEX idx_user_roles_user_id ON user_roles(user_id);

CREATE TABLE IF NOT EXISTS api_keys (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    user_id VARCHAR(26) REFERENCES users(id),
    name VARCHAR(255) NOT NULL,
    key_prefix VARCHAR(8) NOT NULL,  -- First 8 chars for identification
    key_hash VARCHAR(255) NOT NULL,  -- SHA-256 hash of full key
    scopes TEXT[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_api_keys_tenant_id ON api_keys(tenant_id);
CREATE INDEX idx_api_keys_key_prefix ON api_keys(key_prefix);
CREATE INDEX idx_api_keys_status ON api_keys(status);

-- ============================================================================
-- Customer Service Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS customers (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    external_id VARCHAR(255),  -- Client's reference
    type VARCHAR(20) NOT NULL DEFAULT 'individual',  -- individual, business
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, active, suspended, closed

    -- Contact info
    email VARCHAR(255),
    phone VARCHAR(50),

    -- Individual fields
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    date_of_birth DATE,

    -- Business fields
    business_name VARCHAR(255),
    business_type VARCHAR(50),
    tax_id VARCHAR(50),

    -- Address
    address_line1 VARCHAR(255),
    address_line2 VARCHAR(255),
    city VARCHAR(100),
    state VARCHAR(100),
    postal_code VARCHAR(20),
    country VARCHAR(2),  -- ISO 3166-1 alpha-2

    -- KYC status
    kyc_level VARCHAR(20) NOT NULL DEFAULT 'none',  -- none, basic, enhanced, full
    kyc_verified_at TIMESTAMPTZ,

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, external_id)
);

CREATE INDEX idx_customers_tenant_id ON customers(tenant_id);
CREATE INDEX idx_customers_external_id ON customers(tenant_id, external_id);
CREATE INDEX idx_customers_email ON customers(tenant_id, email);
CREATE INDEX idx_customers_status ON customers(status);
CREATE INDEX idx_customers_kyc_level ON customers(kyc_level);

CREATE TABLE IF NOT EXISTS kyc_cases (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    customer_id VARCHAR(26) NOT NULL REFERENCES customers(id),
    level VARCHAR(20) NOT NULL,  -- basic, enhanced, full
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, in_review, approved, rejected, expired

    -- Review info
    reviewer_id VARCHAR(26) REFERENCES users(id),
    reviewed_at TIMESTAMPTZ,
    review_notes TEXT,
    rejection_reason VARCHAR(255),

    -- Expiry
    expires_at TIMESTAMPTZ,

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_kyc_cases_tenant_id ON kyc_cases(tenant_id);
CREATE INDEX idx_kyc_cases_customer_id ON kyc_cases(customer_id);
CREATE INDEX idx_kyc_cases_status ON kyc_cases(status);

CREATE TABLE IF NOT EXISTS kyc_artifacts (
    id VARCHAR(26) PRIMARY KEY,
    case_id VARCHAR(26) NOT NULL REFERENCES kyc_cases(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,  -- id_document, proof_of_address, selfie, etc.
    file_url VARCHAR(500),
    file_hash VARCHAR(64),  -- SHA-256 for integrity
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, verified, rejected
    verification_result JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_kyc_artifacts_case_id ON kyc_artifacts(case_id);

CREATE TABLE IF NOT EXISTS customer_tags (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    customer_id VARCHAR(26) NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    tag VARCHAR(50) NOT NULL,
    value VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(customer_id, tag)
);

CREATE INDEX idx_customer_tags_customer_id ON customer_tags(customer_id);
CREATE INDEX idx_customer_tags_tag ON customer_tags(tenant_id, tag);

-- ============================================================================
-- Rules Engine Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS policy_sets (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(50) NOT NULL,  -- kyc, transaction, fee, routing
    status VARCHAR(20) NOT NULL DEFAULT 'draft',  -- draft, active, archived
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

CREATE INDEX idx_policy_sets_tenant_id ON policy_sets(tenant_id);
CREATE INDEX idx_policy_sets_category ON policy_sets(category);

CREATE TABLE IF NOT EXISTS policy_versions (
    id VARCHAR(26) PRIMARY KEY,
    policy_set_id VARCHAR(26) NOT NULL REFERENCES policy_sets(id),
    version INT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',  -- draft, active, archived
    activated_at TIMESTAMPTZ,
    activated_by VARCHAR(26) REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(policy_set_id, version)
);

CREATE INDEX idx_policy_versions_policy_set_id ON policy_versions(policy_set_id);
CREATE INDEX idx_policy_versions_status ON policy_versions(status);

CREATE TABLE IF NOT EXISTS policy_rules (
    id VARCHAR(26) PRIMARY KEY,
    version_id VARCHAR(26) NOT NULL REFERENCES policy_versions(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    priority INT NOT NULL DEFAULT 0,  -- Higher = evaluated first

    -- Conditions (JSON-based DSL)
    conditions JSONB NOT NULL,

    -- Actions
    action VARCHAR(50) NOT NULL,  -- allow, deny, flag, route, etc.
    action_params JSONB NOT NULL DEFAULT '{}',

    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_policy_rules_version_id ON policy_rules(version_id);
CREATE INDEX idx_policy_rules_priority ON policy_rules(priority DESC);

CREATE TABLE IF NOT EXISTS policy_audit (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    policy_set_id VARCHAR(26) NOT NULL REFERENCES policy_sets(id),
    version_id VARCHAR(26) NOT NULL REFERENCES policy_versions(id),

    -- Evaluation context
    context_type VARCHAR(50) NOT NULL,  -- transaction, customer, payment
    context_id VARCHAR(26) NOT NULL,

    -- Input/Output
    input JSONB NOT NULL,
    matched_rules JSONB NOT NULL,  -- Array of matched rule IDs
    result VARCHAR(50) NOT NULL,
    result_params JSONB,

    evaluation_time_ms INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_policy_audit_tenant_id ON policy_audit(tenant_id);
CREATE INDEX idx_policy_audit_context ON policy_audit(context_type, context_id);
CREATE INDEX idx_policy_audit_created_at ON policy_audit(created_at);

-- ============================================================================
-- Fees & Affiliate Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS fee_programs (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    fee_type VARCHAR(50) NOT NULL,  -- transaction, subscription, service
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

CREATE INDEX idx_fee_programs_tenant_id ON fee_programs(tenant_id);
CREATE INDEX idx_fee_programs_status ON fee_programs(status);

CREATE TABLE IF NOT EXISTS fee_program_versions (
    id VARCHAR(26) PRIMARY KEY,
    program_id VARCHAR(26) NOT NULL REFERENCES fee_programs(id),
    version INT NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL,
    effective_to TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(program_id, version)
);

CREATE INDEX idx_fee_program_versions_program_id ON fee_program_versions(program_id);
CREATE INDEX idx_fee_program_versions_effective ON fee_program_versions(effective_from, effective_to);

CREATE TABLE IF NOT EXISTS fee_rules (
    id VARCHAR(26) PRIMARY KEY,
    version_id VARCHAR(26) NOT NULL REFERENCES fee_program_versions(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,

    -- Matching conditions
    conditions JSONB NOT NULL DEFAULT '{}',

    -- Fee calculation
    calculation_type VARCHAR(20) NOT NULL,  -- flat, percentage, tiered
    flat_amount BIGINT,  -- Minor units
    percentage_bps INT,  -- Basis points (100 = 1%)
    min_amount BIGINT,
    max_amount BIGINT,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',

    priority INT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fee_rules_version_id ON fee_rules(version_id);

CREATE TABLE IF NOT EXISTS fee_tiers (
    id VARCHAR(26) PRIMARY KEY,
    rule_id VARCHAR(26) NOT NULL REFERENCES fee_rules(id) ON DELETE CASCADE,
    min_amount BIGINT NOT NULL,
    max_amount BIGINT,  -- NULL = unlimited
    flat_amount BIGINT,
    percentage_bps INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fee_tiers_rule_id ON fee_tiers(rule_id);

CREATE TABLE IF NOT EXISTS fee_quotes (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    program_id VARCHAR(26) NOT NULL REFERENCES fee_programs(id),

    -- Reference to source transaction
    reference_type VARCHAR(50) NOT NULL,
    reference_id VARCHAR(26) NOT NULL,

    -- Quote details
    base_amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    quoted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,

    -- Status
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, accepted, expired, voided
    accepted_at TIMESTAMPTZ,

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fee_quotes_tenant_id ON fee_quotes(tenant_id);
CREATE INDEX idx_fee_quotes_reference ON fee_quotes(reference_type, reference_id);

CREATE TABLE IF NOT EXISTS fee_items (
    id VARCHAR(26) PRIMARY KEY,
    quote_id VARCHAR(26) NOT NULL REFERENCES fee_quotes(id) ON DELETE CASCADE,
    rule_id VARCHAR(26) NOT NULL REFERENCES fee_rules(id),
    name VARCHAR(255) NOT NULL,
    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    calculation_detail JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fee_items_quote_id ON fee_items(quote_id);

CREATE TABLE IF NOT EXISTS fee_accruals (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    quote_id VARCHAR(26) NOT NULL REFERENCES fee_quotes(id),

    -- Ledger reference
    ledger_batch_id VARCHAR(26),

    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, posted, reversed
    posted_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fee_accruals_tenant_id ON fee_accruals(tenant_id);
CREATE INDEX idx_fee_accruals_quote_id ON fee_accruals(quote_id);

-- Affiliate tables
CREATE TABLE IF NOT EXISTS affiliates (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    customer_id VARCHAR(26) REFERENCES customers(id),

    code VARCHAR(50) NOT NULL,  -- Unique referral code
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255),

    status VARCHAR(20) NOT NULL DEFAULT 'active',

    -- Payout settings
    payout_method VARCHAR(50),  -- bank_transfer, wallet, etc.
    payout_details JSONB NOT NULL DEFAULT '{}',
    min_payout_amount BIGINT NOT NULL DEFAULT 10000,  -- $100 in cents

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_affiliates_tenant_id ON affiliates(tenant_id);
CREATE INDEX idx_affiliates_code ON affiliates(tenant_id, code);

CREATE TABLE IF NOT EXISTS affiliate_relationships (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    affiliate_id VARCHAR(26) NOT NULL REFERENCES affiliates(id),
    customer_id VARCHAR(26) NOT NULL REFERENCES customers(id),

    attributed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attribution_type VARCHAR(50) NOT NULL DEFAULT 'signup',  -- signup, referral_code

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(customer_id)  -- Customer can only have one affiliate
);

CREATE INDEX idx_affiliate_relationships_affiliate_id ON affiliate_relationships(affiliate_id);
CREATE INDEX idx_affiliate_relationships_customer_id ON affiliate_relationships(customer_id);

CREATE TABLE IF NOT EXISTS affiliate_commission_programs (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Commission settings
    commission_type VARCHAR(20) NOT NULL,  -- percentage, flat, tiered
    commission_bps INT,  -- Basis points
    flat_amount BIGINT,

    -- Validity
    effective_from TIMESTAMPTZ NOT NULL,
    effective_to TIMESTAMPTZ,

    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_affiliate_commission_programs_tenant_id ON affiliate_commission_programs(tenant_id);

CREATE TABLE IF NOT EXISTS affiliate_earnings (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    affiliate_id VARCHAR(26) NOT NULL REFERENCES affiliates(id),
    program_id VARCHAR(26) NOT NULL REFERENCES affiliate_commission_programs(id),

    -- Source transaction
    source_type VARCHAR(50) NOT NULL,
    source_id VARCHAR(26) NOT NULL,
    source_amount BIGINT NOT NULL,

    -- Earning
    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, approved, paid, voided
    approved_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_affiliate_earnings_affiliate_id ON affiliate_earnings(affiliate_id);
CREATE INDEX idx_affiliate_earnings_status ON affiliate_earnings(status);

CREATE TABLE IF NOT EXISTS affiliate_payouts (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    affiliate_id VARCHAR(26) NOT NULL REFERENCES affiliates(id),

    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    payout_method VARCHAR(50) NOT NULL,
    payout_reference VARCHAR(255),

    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, processing, completed, failed
    processed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    failure_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_affiliate_payouts_affiliate_id ON affiliate_payouts(affiliate_id);
CREATE INDEX idx_affiliate_payouts_status ON affiliate_payouts(status);

CREATE TABLE IF NOT EXISTS affiliate_payout_items (
    id VARCHAR(26) PRIMARY KEY,
    payout_id VARCHAR(26) NOT NULL REFERENCES affiliate_payouts(id) ON DELETE CASCADE,
    earning_id VARCHAR(26) NOT NULL REFERENCES affiliate_earnings(id),
    amount BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_affiliate_payout_items_payout_id ON affiliate_payout_items(payout_id);

-- ============================================================================
-- Ledger Service Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),

    code VARCHAR(50) NOT NULL,  -- Account code (e.g., 1000, 2000)
    name VARCHAR(255) NOT NULL,
    description TEXT,

    account_type VARCHAR(20) NOT NULL,  -- asset, liability, equity, revenue, expense
    normal_balance VARCHAR(10) NOT NULL,  -- debit, credit

    currency VARCHAR(3) NOT NULL DEFAULT 'USD',

    -- Hierarchy
    parent_id VARCHAR(26) REFERENCES ledger_accounts(id),
    path TEXT NOT NULL,  -- Materialized path for hierarchy queries

    -- Flags
    is_system BOOLEAN NOT NULL DEFAULT false,  -- System accounts can't be deleted
    is_placeholder BOOLEAN NOT NULL DEFAULT false,  -- Placeholder accounts can't have entries

    status VARCHAR(20) NOT NULL DEFAULT 'active',

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, code)
);

CREATE INDEX idx_ledger_accounts_tenant_id ON ledger_accounts(tenant_id);
CREATE INDEX idx_ledger_accounts_type ON ledger_accounts(account_type);
CREATE INDEX idx_ledger_accounts_path ON ledger_accounts(path);
CREATE INDEX idx_ledger_accounts_parent_id ON ledger_accounts(parent_id);

CREATE TABLE IF NOT EXISTS ledger_batches (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),

    -- Batch info
    reference VARCHAR(255),  -- External reference
    description TEXT,

    -- Source
    source_type VARCHAR(50) NOT NULL,  -- deposit, payment, fee, adjustment, etc.
    source_id VARCHAR(26),

    -- Totals (for validation)
    total_debits BIGINT NOT NULL,
    total_credits BIGINT NOT NULL,
    entry_count INT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    -- Status
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, posted, reversed
    posted_at TIMESTAMPTZ,
    posted_by VARCHAR(26) REFERENCES users(id),

    reversed_at TIMESTAMPTZ,
    reversed_by VARCHAR(26) REFERENCES users(id),
    reversal_reason TEXT,

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_batches_tenant_id ON ledger_batches(tenant_id);
CREATE INDEX idx_ledger_batches_source ON ledger_batches(source_type, source_id);
CREATE INDEX idx_ledger_batches_status ON ledger_batches(status);
CREATE INDEX idx_ledger_batches_posted_at ON ledger_batches(posted_at);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id VARCHAR(26) PRIMARY KEY,
    batch_id VARCHAR(26) NOT NULL REFERENCES ledger_batches(id),
    account_id VARCHAR(26) NOT NULL REFERENCES ledger_accounts(id),

    entry_type VARCHAR(10) NOT NULL,  -- debit, credit
    amount BIGINT NOT NULL,  -- Always positive
    currency VARCHAR(3) NOT NULL,

    -- Running balance (updated on post)
    balance_after BIGINT,

    description TEXT,

    -- Sequence within batch
    sequence INT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_entries_batch_id ON ledger_entries(batch_id);
CREATE INDEX idx_ledger_entries_account_id ON ledger_entries(account_id);

-- Position tracking (aggregated balances)
CREATE TABLE IF NOT EXISTS ledger_positions (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    account_id VARCHAR(26) NOT NULL REFERENCES ledger_accounts(id),

    -- Period
    period_type VARCHAR(20) NOT NULL,  -- daily, monthly, yearly
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,

    -- Balances
    opening_balance BIGINT NOT NULL,
    debit_total BIGINT NOT NULL DEFAULT 0,
    credit_total BIGINT NOT NULL DEFAULT 0,
    closing_balance BIGINT NOT NULL,

    entry_count INT NOT NULL DEFAULT 0,

    currency VARCHAR(3) NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(account_id, period_type, period_start)
);

CREATE INDEX idx_ledger_positions_account_id ON ledger_positions(account_id);
CREATE INDEX idx_ledger_positions_period ON ledger_positions(period_start, period_end);

-- ============================================================================
-- Wallet Service Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS wallets (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    customer_id VARCHAR(26) NOT NULL REFERENCES customers(id),

    name VARCHAR(255) NOT NULL DEFAULT 'Primary Wallet',
    type VARCHAR(20) NOT NULL DEFAULT 'standard',  -- standard, merchant, escrow

    status VARCHAR(20) NOT NULL DEFAULT 'active',  -- active, suspended, closed

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallets_tenant_id ON wallets(tenant_id);
CREATE INDEX idx_wallets_customer_id ON wallets(customer_id);

CREATE TABLE IF NOT EXISTS wallet_accounts (
    id VARCHAR(26) PRIMARY KEY,
    wallet_id VARCHAR(26) NOT NULL REFERENCES wallets(id),

    currency VARCHAR(3) NOT NULL,

    -- Ledger link
    ledger_account_id VARCHAR(26) REFERENCES ledger_accounts(id),

    status VARCHAR(20) NOT NULL DEFAULT 'active',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(wallet_id, currency)
);

CREATE INDEX idx_wallet_accounts_wallet_id ON wallet_accounts(wallet_id);

-- Balance cache (denormalized for fast reads)
CREATE TABLE IF NOT EXISTS wallet_balance_cache (
    id VARCHAR(26) PRIMARY KEY,
    wallet_account_id VARCHAR(26) NOT NULL REFERENCES wallet_accounts(id),

    available_balance BIGINT NOT NULL DEFAULT 0,
    pending_balance BIGINT NOT NULL DEFAULT 0,  -- Incoming, not yet confirmed
    held_balance BIGINT NOT NULL DEFAULT 0,     -- Held for pending debits

    -- Total = available + pending + held

    currency VARCHAR(3) NOT NULL,

    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(wallet_account_id)
);

CREATE INDEX idx_wallet_balance_cache_wallet_account_id ON wallet_balance_cache(wallet_account_id);

CREATE TABLE IF NOT EXISTS wallet_holds (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    wallet_account_id VARCHAR(26) NOT NULL REFERENCES wallet_accounts(id),

    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    -- Reference
    reference_type VARCHAR(50) NOT NULL,  -- payment, withdrawal, etc.
    reference_id VARCHAR(26) NOT NULL,

    reason VARCHAR(255),

    status VARCHAR(20) NOT NULL DEFAULT 'active',  -- active, captured, released, expired

    expires_at TIMESTAMPTZ,
    captured_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,

    -- Ledger reference when captured
    ledger_batch_id VARCHAR(26) REFERENCES ledger_batches(id),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallet_holds_wallet_account_id ON wallet_holds(wallet_account_id);
CREATE INDEX idx_wallet_holds_reference ON wallet_holds(reference_type, reference_id);
CREATE INDEX idx_wallet_holds_status ON wallet_holds(status);
CREATE INDEX idx_wallet_holds_expires_at ON wallet_holds(expires_at) WHERE status = 'active';

-- ============================================================================
-- Deposits Service Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS deposits (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),

    -- Source info
    source VARCHAR(50) NOT NULL,  -- bank_transfer, card, crypto, etc.
    source_reference VARCHAR(255),  -- External reference from source

    -- Amount
    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    -- Sender info (from bank/source)
    sender_name VARCHAR(255),
    sender_account VARCHAR(255),
    sender_bank_code VARCHAR(50),

    -- Matching info
    matching_reference VARCHAR(255),  -- Reference used for matching

    status VARCHAR(20) NOT NULL DEFAULT 'received',  -- received, matched, credited, returned

    -- Timestamps
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    matched_at TIMESTAMPTZ,
    credited_at TIMESTAMPTZ,
    returned_at TIMESTAMPTZ,

    return_reason VARCHAR(255),

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deposits_tenant_id ON deposits(tenant_id);
CREATE INDEX idx_deposits_source ON deposits(source);
CREATE INDEX idx_deposits_status ON deposits(status);
CREATE INDEX idx_deposits_matching_reference ON deposits(matching_reference);
CREATE INDEX idx_deposits_received_at ON deposits(received_at);

CREATE TABLE IF NOT EXISTS deposit_matches (
    id VARCHAR(26) PRIMARY KEY,
    deposit_id VARCHAR(26) NOT NULL REFERENCES deposits(id),

    -- What it matched to
    match_type VARCHAR(50) NOT NULL,  -- customer, wallet, virtual_account
    customer_id VARCHAR(26) REFERENCES customers(id),
    wallet_id VARCHAR(26) REFERENCES wallets(id),

    -- Match details
    match_confidence DECIMAL(5, 2) NOT NULL,  -- 0-100
    match_method VARCHAR(50) NOT NULL,  -- reference, name, account
    match_details JSONB NOT NULL DEFAULT '{}',

    -- Review
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, confirmed, rejected
    reviewed_by VARCHAR(26) REFERENCES users(id),
    reviewed_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deposit_matches_deposit_id ON deposit_matches(deposit_id);
CREATE INDEX idx_deposit_matches_customer_id ON deposit_matches(customer_id);

CREATE TABLE IF NOT EXISTS deposit_credits (
    id VARCHAR(26) PRIMARY KEY,
    deposit_id VARCHAR(26) NOT NULL REFERENCES deposits(id),
    match_id VARCHAR(26) NOT NULL REFERENCES deposit_matches(id),

    wallet_account_id VARCHAR(26) NOT NULL REFERENCES wallet_accounts(id),

    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    -- Ledger reference
    ledger_batch_id VARCHAR(26) REFERENCES ledger_batches(id),

    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, posted, reversed
    posted_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deposit_credits_deposit_id ON deposit_credits(deposit_id);
CREATE INDEX idx_deposit_credits_wallet_account_id ON deposit_credits(wallet_account_id);

-- ============================================================================
-- Payments Service Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS payment_intents (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),
    customer_id VARCHAR(26) NOT NULL REFERENCES customers(id),

    -- Idempotency
    idempotency_key VARCHAR(255),

    -- Amount
    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    -- Destination
    destination_type VARCHAR(50) NOT NULL,  -- bank_account, wallet, card, etc.
    destination_details JSONB NOT NULL,

    -- Beneficiary
    beneficiary_name VARCHAR(255) NOT NULL,
    beneficiary_reference VARCHAR(255),

    -- Purpose
    purpose VARCHAR(100),
    description TEXT,

    -- Source wallet
    source_wallet_id VARCHAR(26) REFERENCES wallets(id),

    -- Fee handling
    fee_quote_id VARCHAR(26) REFERENCES fee_quotes(id),
    fee_amount BIGINT,
    fee_currency VARCHAR(3),

    -- Status
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- pending, processing, routing, submitted, completed, failed, cancelled

    failure_reason VARCHAR(255),
    failure_code VARCHAR(50),

    -- Timestamps
    submitted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,

    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, idempotency_key)
);

CREATE INDEX idx_payment_intents_tenant_id ON payment_intents(tenant_id);
CREATE INDEX idx_payment_intents_customer_id ON payment_intents(customer_id);
CREATE INDEX idx_payment_intents_status ON payment_intents(status);
CREATE INDEX idx_payment_intents_created_at ON payment_intents(created_at);

CREATE TABLE IF NOT EXISTS payment_routes (
    id VARCHAR(26) PRIMARY KEY,
    intent_id VARCHAR(26) NOT NULL REFERENCES payment_intents(id),

    -- Selected route
    provider VARCHAR(50) NOT NULL,  -- bank_rails, swift, ach, etc.
    rail VARCHAR(50) NOT NULL,

    -- Route details
    estimated_arrival TIMESTAMPTZ,
    provider_fee BIGINT,

    -- Routing decision
    routing_rule_id VARCHAR(26),  -- Reference to policy rule
    routing_details JSONB NOT NULL DEFAULT '{}',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_routes_intent_id ON payment_routes(intent_id);

CREATE TABLE IF NOT EXISTS payment_reservations (
    id VARCHAR(26) PRIMARY KEY,
    intent_id VARCHAR(26) NOT NULL REFERENCES payment_intents(id),

    -- Hold reference
    wallet_hold_id VARCHAR(26) REFERENCES wallet_holds(id),

    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'active',  -- active, captured, released

    captured_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_reservations_intent_id ON payment_reservations(intent_id);

CREATE TABLE IF NOT EXISTS payment_submissions (
    id VARCHAR(26) PRIMARY KEY,
    intent_id VARCHAR(26) NOT NULL REFERENCES payment_intents(id),
    route_id VARCHAR(26) NOT NULL REFERENCES payment_routes(id),

    -- Provider details
    provider VARCHAR(50) NOT NULL,
    provider_reference VARCHAR(255),

    -- Submission
    request_payload JSONB,
    response_payload JSONB,

    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- pending, submitted, accepted, rejected, completed, failed

    submitted_at TIMESTAMPTZ,
    response_at TIMESTAMPTZ,

    error_code VARCHAR(50),
    error_message TEXT,

    -- Retry tracking
    attempt_number INT NOT NULL DEFAULT 1,
    next_retry_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_submissions_intent_id ON payment_submissions(intent_id);
CREATE INDEX idx_payment_submissions_provider ON payment_submissions(provider);
CREATE INDEX idx_payment_submissions_status ON payment_submissions(status);

CREATE TABLE IF NOT EXISTS payment_webhooks (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),

    -- Source
    provider VARCHAR(50) NOT NULL,
    event_type VARCHAR(100) NOT NULL,

    -- Payload
    payload JSONB NOT NULL,
    headers JSONB NOT NULL DEFAULT '{}',

    -- Processing
    status VARCHAR(20) NOT NULL DEFAULT 'received',  -- received, processing, processed, failed
    processed_at TIMESTAMPTZ,

    -- Linked payment
    intent_id VARCHAR(26) REFERENCES payment_intents(id),
    submission_id VARCHAR(26) REFERENCES payment_submissions(id),

    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_webhooks_tenant_id ON payment_webhooks(tenant_id);
CREATE INDEX idx_payment_webhooks_provider ON payment_webhooks(provider);
CREATE INDEX idx_payment_webhooks_status ON payment_webhooks(status);
CREATE INDEX idx_payment_webhooks_created_at ON payment_webhooks(created_at);

-- ============================================================================
-- Outbox (for reliable event publishing)
-- ============================================================================

CREATE TABLE IF NOT EXISTS outbox_events (
    id VARCHAR(26) PRIMARY KEY,
    tenant_id VARCHAR(26) NOT NULL REFERENCES tenants(id),

    -- Event info
    event_id VARCHAR(26) NOT NULL,
    event_type VARCHAR(100) NOT NULL,
    aggregate_type VARCHAR(50) NOT NULL,
    aggregate_id VARCHAR(26) NOT NULL,

    -- Payload
    payload JSONB NOT NULL,

    -- Publishing status
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, published, failed
    published_at TIMESTAMPTZ,

    -- Retry tracking
    attempts INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_error TEXT,
    next_retry_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_outbox_events_status ON outbox_events(status);
CREATE INDEX idx_outbox_events_next_retry ON outbox_events(next_retry_at) WHERE status = 'pending';
CREATE INDEX idx_outbox_events_created_at ON outbox_events(created_at);

-- ============================================================================
-- Utility Functions
-- ============================================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply updated_at triggers
CREATE TRIGGER update_tenants_updated_at BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_customers_updated_at BEFORE UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_kyc_cases_updated_at BEFORE UPDATE ON kyc_cases
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_policy_sets_updated_at BEFORE UPDATE ON policy_sets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_fee_programs_updated_at BEFORE UPDATE ON fee_programs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_affiliates_updated_at BEFORE UPDATE ON affiliates
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_affiliate_commission_programs_updated_at BEFORE UPDATE ON affiliate_commission_programs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_ledger_accounts_updated_at BEFORE UPDATE ON ledger_accounts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_ledger_positions_updated_at BEFORE UPDATE ON ledger_positions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_wallets_updated_at BEFORE UPDATE ON wallets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_wallet_accounts_updated_at BEFORE UPDATE ON wallet_accounts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_wallet_holds_updated_at BEFORE UPDATE ON wallet_holds
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_deposits_updated_at BEFORE UPDATE ON deposits
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_payment_intents_updated_at BEFORE UPDATE ON payment_intents
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
