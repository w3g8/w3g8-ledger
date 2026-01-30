package fps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements the FPS Store interface with PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgreSQL FPS store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Create inserts a new FPS payment record.
func (s *PostgresStore) Create(ctx context.Context, payment *FPSPayment) error {
	responseData, err := json.Marshal(payment.ResponseData)
	if err != nil {
		responseData = []byte("{}")
	}

	query := `
		INSERT INTO fps_payments (
			id, payment_attempt_id, end_to_end_id, provider_payment_id,
			sort_code, account_number, fps_status,
			submitted_at, accepted_at, settled_at,
			error_code, error_message, response_data,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`

	_, err = s.pool.Exec(ctx, query,
		payment.ID,
		payment.PaymentAttemptID,
		payment.EndToEndID,
		nullableString(payment.ProviderPaymentID),
		nullableString(payment.SortCode),
		nullableString(payment.AccountNumber),
		payment.Status,
		payment.SubmittedAt,
		payment.AcceptedAt,
		payment.SettledAt,
		nullableString(payment.ErrorCode),
		nullableString(payment.ErrorMessage),
		responseData,
		payment.CreatedAt,
		payment.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert fps payment: %w", err)
	}

	return nil
}

// GetByEndToEndID retrieves an FPS payment by end-to-end ID.
func (s *PostgresStore) GetByEndToEndID(ctx context.Context, endToEndID string) (*FPSPayment, error) {
	query := `
		SELECT id, payment_attempt_id, end_to_end_id, provider_payment_id,
			   sort_code, account_number, fps_status,
			   submitted_at, accepted_at, settled_at,
			   error_code, error_message, response_data,
			   created_at, updated_at
		FROM fps_payments
		WHERE end_to_end_id = $1
	`

	row := s.pool.QueryRow(ctx, query, endToEndID)
	return s.scanPayment(row)
}

// GetByPaymentAttemptID retrieves an FPS payment by attempt ID.
func (s *PostgresStore) GetByPaymentAttemptID(ctx context.Context, attemptID string) (*FPSPayment, error) {
	query := `
		SELECT id, payment_attempt_id, end_to_end_id, provider_payment_id,
			   sort_code, account_number, fps_status,
			   submitted_at, accepted_at, settled_at,
			   error_code, error_message, response_data,
			   created_at, updated_at
		FROM fps_payments
		WHERE payment_attempt_id = $1
	`

	row := s.pool.QueryRow(ctx, query, attemptID)
	return s.scanPayment(row)
}

// UpdateStatus updates the FPS payment status.
func (s *PostgresStore) UpdateStatus(ctx context.Context, endToEndID string, status FPSStatus, providerPaymentID string, responseData map[string]any) error {
	respDataJSON, err := json.Marshal(responseData)
	if err != nil {
		respDataJSON = []byte("{}")
	}

	query := `
		UPDATE fps_payments
		SET fps_status = $2, provider_payment_id = $3, response_data = $4
		WHERE end_to_end_id = $1
	`

	result, err := s.pool.Exec(ctx, query, endToEndID, status, nullableString(providerPaymentID), respDataJSON)
	if err != nil {
		return fmt.Errorf("update fps payment status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("fps payment not found: %s", endToEndID)
	}

	return nil
}

// MarkAccepted marks the FPS payment as accepted.
func (s *PostgresStore) MarkAccepted(ctx context.Context, endToEndID string, acceptedAt time.Time) error {
	query := `
		UPDATE fps_payments
		SET fps_status = $2, accepted_at = $3
		WHERE end_to_end_id = $1
	`

	result, err := s.pool.Exec(ctx, query, endToEndID, FPSAccepted, acceptedAt)
	if err != nil {
		return fmt.Errorf("mark fps payment accepted: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("fps payment not found: %s", endToEndID)
	}

	return nil
}

// MarkSettled marks the FPS payment as settled.
func (s *PostgresStore) MarkSettled(ctx context.Context, endToEndID string, settledAt time.Time) error {
	query := `
		UPDATE fps_payments
		SET fps_status = $2, settled_at = $3
		WHERE end_to_end_id = $1
	`

	result, err := s.pool.Exec(ctx, query, endToEndID, FPSSettled, settledAt)
	if err != nil {
		return fmt.Errorf("mark fps payment settled: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("fps payment not found: %s", endToEndID)
	}

	return nil
}

// MarkFailed marks the FPS payment as failed.
func (s *PostgresStore) MarkFailed(ctx context.Context, endToEndID string, errorCode, errorMessage string) error {
	query := `
		UPDATE fps_payments
		SET fps_status = $2, error_code = $3, error_message = $4
		WHERE end_to_end_id = $1
	`

	result, err := s.pool.Exec(ctx, query, endToEndID, FPSFailed, errorCode, errorMessage)
	if err != nil {
		return fmt.Errorf("mark fps payment failed: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("fps payment not found: %s", endToEndID)
	}

	return nil
}

// GetPendingPayments retrieves FPS payments in SUBMITTED or ACCEPTED status.
func (s *PostgresStore) GetPendingPayments(ctx context.Context, olderThan time.Duration, limit int) ([]*FPSPayment, error) {
	cutoff := time.Now().Add(-olderThan)

	query := `
		SELECT id, payment_attempt_id, end_to_end_id, provider_payment_id,
			   sort_code, account_number, fps_status,
			   submitted_at, accepted_at, settled_at,
			   error_code, error_message, response_data,
			   created_at, updated_at
		FROM fps_payments
		WHERE fps_status IN ('SUBMITTED', 'ACCEPTED')
		  AND submitted_at < $1
		ORDER BY submitted_at ASC
		LIMIT $2
	`

	rows, err := s.pool.Query(ctx, query, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending fps payments: %w", err)
	}
	defer rows.Close()

	var payments []*FPSPayment
	for rows.Next() {
		payment, err := s.scanPaymentRow(rows)
		if err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}

	return payments, rows.Err()
}

func (s *PostgresStore) scanPayment(row pgx.Row) (*FPSPayment, error) {
	var payment FPSPayment
	var providerPaymentID, sortCode, accountNumber, errorCode, errorMessage *string
	var responseDataJSON []byte

	err := row.Scan(
		&payment.ID,
		&payment.PaymentAttemptID,
		&payment.EndToEndID,
		&providerPaymentID,
		&sortCode,
		&accountNumber,
		&payment.Status,
		&payment.SubmittedAt,
		&payment.AcceptedAt,
		&payment.SettledAt,
		&errorCode,
		&errorMessage,
		&responseDataJSON,
		&payment.CreatedAt,
		&payment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("fps payment not found")
		}
		return nil, fmt.Errorf("scan fps payment: %w", err)
	}

	if providerPaymentID != nil {
		payment.ProviderPaymentID = *providerPaymentID
	}
	if sortCode != nil {
		payment.SortCode = *sortCode
	}
	if accountNumber != nil {
		payment.AccountNumber = *accountNumber
	}
	if errorCode != nil {
		payment.ErrorCode = *errorCode
	}
	if errorMessage != nil {
		payment.ErrorMessage = *errorMessage
	}

	if len(responseDataJSON) > 0 {
		json.Unmarshal(responseDataJSON, &payment.ResponseData)
	}

	return &payment, nil
}

func (s *PostgresStore) scanPaymentRow(rows pgx.Rows) (*FPSPayment, error) {
	var payment FPSPayment
	var providerPaymentID, sortCode, accountNumber, errorCode, errorMessage *string
	var responseDataJSON []byte

	err := rows.Scan(
		&payment.ID,
		&payment.PaymentAttemptID,
		&payment.EndToEndID,
		&providerPaymentID,
		&sortCode,
		&accountNumber,
		&payment.Status,
		&payment.SubmittedAt,
		&payment.AcceptedAt,
		&payment.SettledAt,
		&errorCode,
		&errorMessage,
		&responseDataJSON,
		&payment.CreatedAt,
		&payment.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan fps payment row: %w", err)
	}

	if providerPaymentID != nil {
		payment.ProviderPaymentID = *providerPaymentID
	}
	if sortCode != nil {
		payment.SortCode = *sortCode
	}
	if accountNumber != nil {
		payment.AccountNumber = *accountNumber
	}
	if errorCode != nil {
		payment.ErrorCode = *errorCode
	}
	if errorMessage != nil {
		payment.ErrorMessage = *errorMessage
	}

	if len(responseDataJSON) > 0 {
		json.Unmarshal(responseDataJSON, &payment.ResponseData)
	}

	return &payment, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
