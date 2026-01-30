package funding

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgreSQL store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// CreateIntent inserts a new funding intent.
func (s *PostgresStore) CreateIntent(ctx context.Context, intent *FundingIntent) error {
	query := `
		INSERT INTO funding_intents (
			id, tenant_id, wallet_id, customer_id,
			amount_minor, currency, method, status, idempotency_key,
			provider_ref, redirect_url, bank_details, payment_session,
			attempt_count, last_attempt_at, settled_at, reversed_at, reversal_reason,
			ledger_batch_id, metadata, error_code, error_message,
			created_at, updated_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25
		)
	`

	bankDetails, _ := json.Marshal(intent.BankDetails)
	metadata, _ := json.Marshal(intent.Metadata)

	_, err := s.pool.Exec(ctx, query,
		intent.ID, intent.TenantID, intent.WalletID, intent.CustomerID,
		intent.Amount.AmountMinor, intent.Amount.Currency, intent.Method, intent.Status, intent.IdempotencyKey,
		nullStr(intent.ProviderRef), nullStr(intent.RedirectURL), bankDetails, nullStr(intent.PaymentSession),
		intent.AttemptCount, intent.LastAttemptAt, intent.SettledAt, intent.ReversedAt, nullStr(intent.ReversalReason),
		nullStr(intent.LedgerBatchID), metadata, nullStr(intent.ErrorCode), nullStr(intent.ErrorMessage),
		intent.CreatedAt, intent.UpdatedAt, intent.ExpiresAt,
	)
	return err
}

// GetIntent retrieves a funding intent by ID.
func (s *PostgresStore) GetIntent(ctx context.Context, tenantID, intentID string) (*FundingIntent, error) {
	query := `
		SELECT id, tenant_id, wallet_id, customer_id,
			   amount_minor, currency, method, status, idempotency_key,
			   provider_ref, redirect_url, bank_details, payment_session,
			   attempt_count, last_attempt_at, settled_at, reversed_at, reversal_reason,
			   ledger_batch_id, metadata, error_code, error_message,
			   created_at, updated_at, expires_at
		FROM funding_intents
		WHERE id = $1 AND (tenant_id = $2 OR $2 = '')
	`

	row := s.pool.QueryRow(ctx, query, intentID, tenantID)
	return s.scanIntent(row)
}

// GetIntentByIdempotencyKey retrieves a funding intent by idempotency key.
func (s *PostgresStore) GetIntentByIdempotencyKey(ctx context.Context, tenantID, key string) (*FundingIntent, error) {
	query := `
		SELECT id, tenant_id, wallet_id, customer_id,
			   amount_minor, currency, method, status, idempotency_key,
			   provider_ref, redirect_url, bank_details, payment_session,
			   attempt_count, last_attempt_at, settled_at, reversed_at, reversal_reason,
			   ledger_batch_id, metadata, error_code, error_message,
			   created_at, updated_at, expires_at
		FROM funding_intents
		WHERE tenant_id = $1 AND idempotency_key = $2
	`

	row := s.pool.QueryRow(ctx, query, tenantID, key)
	return s.scanIntent(row)
}

// GetIntentByReference retrieves a funding intent by bank reference.
func (s *PostgresStore) GetIntentByReference(ctx context.Context, tenantID, reference string) (*FundingIntent, error) {
	query := `
		SELECT id, tenant_id, wallet_id, customer_id,
			   amount_minor, currency, method, status, idempotency_key,
			   provider_ref, redirect_url, bank_details, payment_session,
			   attempt_count, last_attempt_at, settled_at, reversed_at, reversal_reason,
			   ledger_batch_id, metadata, error_code, error_message,
			   created_at, updated_at, expires_at
		FROM funding_intents
		WHERE tenant_id = $1 AND bank_details->>'reference' = $2
	`

	row := s.pool.QueryRow(ctx, query, tenantID, reference)
	return s.scanIntent(row)
}

// UpdateIntent updates a funding intent.
func (s *PostgresStore) UpdateIntent(ctx context.Context, intent *FundingIntent) error {
	query := `
		UPDATE funding_intents SET
			status = $2, provider_ref = $3, redirect_url = $4, payment_session = $5,
			attempt_count = $6, last_attempt_at = $7, settled_at = $8, reversed_at = $9,
			reversal_reason = $10, ledger_batch_id = $11, error_code = $12, error_message = $13,
			updated_at = $14
		WHERE id = $1
	`

	intent.UpdatedAt = time.Now().UTC()

	_, err := s.pool.Exec(ctx, query,
		intent.ID, intent.Status, nullStr(intent.ProviderRef), nullStr(intent.RedirectURL),
		nullStr(intent.PaymentSession), intent.AttemptCount, intent.LastAttemptAt,
		intent.SettledAt, intent.ReversedAt, nullStr(intent.ReversalReason),
		nullStr(intent.LedgerBatchID), nullStr(intent.ErrorCode), nullStr(intent.ErrorMessage),
		intent.UpdatedAt,
	)
	return err
}

// ListPendingIntents lists pending intents older than a given duration.
func (s *PostgresStore) ListPendingIntents(ctx context.Context, tenantID string, olderThan time.Duration, limit int) ([]*FundingIntent, error) {
	query := `
		SELECT id, tenant_id, wallet_id, customer_id,
			   amount_minor, currency, method, status, idempotency_key,
			   provider_ref, redirect_url, bank_details, payment_session,
			   attempt_count, last_attempt_at, settled_at, reversed_at, reversal_reason,
			   ledger_batch_id, metadata, error_code, error_message,
			   created_at, updated_at, expires_at
		FROM funding_intents
		WHERE tenant_id = $1 AND status = 'pending' AND created_at < $2
		ORDER BY created_at ASC
		LIMIT $3
	`

	cutoff := time.Now().Add(-olderThan)
	rows, err := s.pool.Query(ctx, query, tenantID, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var intents []*FundingIntent
	for rows.Next() {
		intent, err := s.scanIntentFromRows(rows)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

// CreateAttempt inserts a new funding attempt.
func (s *PostgresStore) CreateAttempt(ctx context.Context, attempt *FundingAttempt) error {
	query := `
		INSERT INTO funding_attempts (
			id, intent_id, provider, provider_ref, status,
			attempt_number, error_code, error_message, provider_data,
			created_at, updated_at, submitted_at, settled_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	providerData, _ := json.Marshal(attempt.ProviderData)

	_, err := s.pool.Exec(ctx, query,
		attempt.ID, attempt.IntentID, attempt.Provider, nullStr(attempt.ProviderRef),
		attempt.Status, attempt.AttemptNumber, nullStr(attempt.ErrorCode), nullStr(attempt.ErrorMessage),
		providerData, attempt.CreatedAt, attempt.UpdatedAt, attempt.SubmittedAt, attempt.SettledAt,
	)
	return err
}

// GetAttempt retrieves a funding attempt by ID.
func (s *PostgresStore) GetAttempt(ctx context.Context, attemptID string) (*FundingAttempt, error) {
	query := `
		SELECT id, intent_id, provider, provider_ref, status,
			   attempt_number, error_code, error_message, provider_data,
			   created_at, updated_at, submitted_at, settled_at
		FROM funding_attempts WHERE id = $1
	`

	row := s.pool.QueryRow(ctx, query, attemptID)

	var a FundingAttempt
	var providerRef, errorCode, errorMsg *string
	var providerData []byte

	err := row.Scan(
		&a.ID, &a.IntentID, &a.Provider, &providerRef, &a.Status,
		&a.AttemptNumber, &errorCode, &errorMsg, &providerData,
		&a.CreatedAt, &a.UpdatedAt, &a.SubmittedAt, &a.SettledAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("attempt not found: %s", attemptID)
		}
		return nil, err
	}

	if providerRef != nil {
		a.ProviderRef = *providerRef
	}
	if errorCode != nil {
		a.ErrorCode = *errorCode
	}
	if errorMsg != nil {
		a.ErrorMessage = *errorMsg
	}
	json.Unmarshal(providerData, &a.ProviderData)

	return &a, nil
}

// UpdateAttempt updates a funding attempt.
func (s *PostgresStore) UpdateAttempt(ctx context.Context, attempt *FundingAttempt) error {
	query := `
		UPDATE funding_attempts SET
			provider_ref = $2, status = $3, error_code = $4, error_message = $5,
			provider_data = $6, updated_at = $7, submitted_at = $8, settled_at = $9
		WHERE id = $1
	`

	providerData, _ := json.Marshal(attempt.ProviderData)
	attempt.UpdatedAt = time.Now().UTC()

	_, err := s.pool.Exec(ctx, query,
		attempt.ID, nullStr(attempt.ProviderRef), attempt.Status,
		nullStr(attempt.ErrorCode), nullStr(attempt.ErrorMessage),
		providerData, attempt.UpdatedAt, attempt.SubmittedAt, attempt.SettledAt,
	)
	return err
}

// ListAttempts lists attempts for an intent.
func (s *PostgresStore) ListAttempts(ctx context.Context, intentID string) ([]*FundingAttempt, error) {
	query := `
		SELECT id, intent_id, provider, provider_ref, status,
			   attempt_number, error_code, error_message, provider_data,
			   created_at, updated_at, submitted_at, settled_at
		FROM funding_attempts WHERE intent_id = $1
		ORDER BY attempt_number ASC
	`

	rows, err := s.pool.Query(ctx, query, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []*FundingAttempt
	for rows.Next() {
		var a FundingAttempt
		var providerRef, errorCode, errorMsg *string
		var providerData []byte

		err := rows.Scan(
			&a.ID, &a.IntentID, &a.Provider, &providerRef, &a.Status,
			&a.AttemptNumber, &errorCode, &errorMsg, &providerData,
			&a.CreatedAt, &a.UpdatedAt, &a.SubmittedAt, &a.SettledAt,
		)
		if err != nil {
			return nil, err
		}

		if providerRef != nil {
			a.ProviderRef = *providerRef
		}
		if errorCode != nil {
			a.ErrorCode = *errorCode
		}
		if errorMsg != nil {
			a.ErrorMessage = *errorMsg
		}
		json.Unmarshal(providerData, &a.ProviderData)

		attempts = append(attempts, &a)
	}
	return attempts, nil
}

func (s *PostgresStore) scanIntent(row pgx.Row) (*FundingIntent, error) {
	var i FundingIntent
	var providerRef, redirectURL, paymentSession *string
	var reversalReason, ledgerBatchID, errorCode, errorMsg *string
	var bankDetails, metadata []byte

	err := row.Scan(
		&i.ID, &i.TenantID, &i.WalletID, &i.CustomerID,
		&i.Amount.AmountMinor, &i.Amount.Currency, &i.Method, &i.Status, &i.IdempotencyKey,
		&providerRef, &redirectURL, &bankDetails, &paymentSession,
		&i.AttemptCount, &i.LastAttemptAt, &i.SettledAt, &i.ReversedAt, &reversalReason,
		&ledgerBatchID, &metadata, &errorCode, &errorMsg,
		&i.CreatedAt, &i.UpdatedAt, &i.ExpiresAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("intent not found")
		}
		return nil, err
	}

	if providerRef != nil {
		i.ProviderRef = *providerRef
	}
	if redirectURL != nil {
		i.RedirectURL = *redirectURL
	}
	if paymentSession != nil {
		i.PaymentSession = *paymentSession
	}
	if reversalReason != nil {
		i.ReversalReason = *reversalReason
	}
	if ledgerBatchID != nil {
		i.LedgerBatchID = *ledgerBatchID
	}
	if errorCode != nil {
		i.ErrorCode = *errorCode
	}
	if errorMsg != nil {
		i.ErrorMessage = *errorMsg
	}

	json.Unmarshal(bankDetails, &i.BankDetails)
	json.Unmarshal(metadata, &i.Metadata)

	return &i, nil
}

func (s *PostgresStore) scanIntentFromRows(rows pgx.Rows) (*FundingIntent, error) {
	var i FundingIntent
	var providerRef, redirectURL, paymentSession *string
	var reversalReason, ledgerBatchID, errorCode, errorMsg *string
	var bankDetails, metadata []byte

	err := rows.Scan(
		&i.ID, &i.TenantID, &i.WalletID, &i.CustomerID,
		&i.Amount.AmountMinor, &i.Amount.Currency, &i.Method, &i.Status, &i.IdempotencyKey,
		&providerRef, &redirectURL, &bankDetails, &paymentSession,
		&i.AttemptCount, &i.LastAttemptAt, &i.SettledAt, &i.ReversedAt, &reversalReason,
		&ledgerBatchID, &metadata, &errorCode, &errorMsg,
		&i.CreatedAt, &i.UpdatedAt, &i.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}

	if providerRef != nil {
		i.ProviderRef = *providerRef
	}
	if redirectURL != nil {
		i.RedirectURL = *redirectURL
	}
	if paymentSession != nil {
		i.PaymentSession = *paymentSession
	}
	if reversalReason != nil {
		i.ReversalReason = *reversalReason
	}
	if ledgerBatchID != nil {
		i.LedgerBatchID = *ledgerBatchID
	}
	if errorCode != nil {
		i.ErrorCode = *errorCode
	}
	if errorMsg != nil {
		i.ErrorMessage = *errorMsg
	}

	json.Unmarshal(bankDetails, &i.BankDetails)
	json.Unmarshal(metadata, &i.Metadata)

	return &i, nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
