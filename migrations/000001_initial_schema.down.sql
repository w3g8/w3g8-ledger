-- Drop triggers first
DROP TRIGGER IF EXISTS update_payment_intents_updated_at ON payment_intents;
DROP TRIGGER IF EXISTS update_deposits_updated_at ON deposits;
DROP TRIGGER IF EXISTS update_wallet_holds_updated_at ON wallet_holds;
DROP TRIGGER IF EXISTS update_wallet_accounts_updated_at ON wallet_accounts;
DROP TRIGGER IF EXISTS update_wallets_updated_at ON wallets;
DROP TRIGGER IF EXISTS update_ledger_positions_updated_at ON ledger_positions;
DROP TRIGGER IF EXISTS update_ledger_accounts_updated_at ON ledger_accounts;
DROP TRIGGER IF EXISTS update_affiliate_commission_programs_updated_at ON affiliate_commission_programs;
DROP TRIGGER IF EXISTS update_affiliates_updated_at ON affiliates;
DROP TRIGGER IF EXISTS update_fee_programs_updated_at ON fee_programs;
DROP TRIGGER IF EXISTS update_policy_sets_updated_at ON policy_sets;
DROP TRIGGER IF EXISTS update_kyc_cases_updated_at ON kyc_cases;
DROP TRIGGER IF EXISTS update_customers_updated_at ON customers;
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
DROP TRIGGER IF EXISTS update_tenants_updated_at ON tenants;

DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop tables in reverse dependency order
DROP TABLE IF EXISTS outbox_events;

DROP TABLE IF EXISTS payment_webhooks;
DROP TABLE IF EXISTS payment_submissions;
DROP TABLE IF EXISTS payment_reservations;
DROP TABLE IF EXISTS payment_routes;
DROP TABLE IF EXISTS payment_intents;

DROP TABLE IF EXISTS deposit_credits;
DROP TABLE IF EXISTS deposit_matches;
DROP TABLE IF EXISTS deposits;

DROP TABLE IF EXISTS wallet_holds;
DROP TABLE IF EXISTS wallet_balance_cache;
DROP TABLE IF EXISTS wallet_accounts;
DROP TABLE IF EXISTS wallets;

DROP TABLE IF EXISTS ledger_positions;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_batches;
DROP TABLE IF EXISTS ledger_accounts;

DROP TABLE IF EXISTS affiliate_payout_items;
DROP TABLE IF EXISTS affiliate_payouts;
DROP TABLE IF EXISTS affiliate_earnings;
DROP TABLE IF EXISTS affiliate_commission_programs;
DROP TABLE IF EXISTS affiliate_relationships;
DROP TABLE IF EXISTS affiliates;

DROP TABLE IF EXISTS fee_accruals;
DROP TABLE IF EXISTS fee_items;
DROP TABLE IF EXISTS fee_quotes;
DROP TABLE IF EXISTS fee_tiers;
DROP TABLE IF EXISTS fee_rules;
DROP TABLE IF EXISTS fee_program_versions;
DROP TABLE IF EXISTS fee_programs;

DROP TABLE IF EXISTS policy_audit;
DROP TABLE IF EXISTS policy_rules;
DROP TABLE IF EXISTS policy_versions;
DROP TABLE IF EXISTS policy_sets;

DROP TABLE IF EXISTS customer_tags;
DROP TABLE IF EXISTS kyc_artifacts;
DROP TABLE IF EXISTS kyc_cases;
DROP TABLE IF EXISTS customers;

DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
