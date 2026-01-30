package sepa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements the SEPA Store interface with PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgreSQL SEPA store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Create inserts a new SEPA payment record.
func (s *PostgresStore) Create(ctx context.Context, payment *SEPAPayment) error {
	responseData, err := json.Marshal(payment.ResponseData)
	if err != nil {
		responseData = []byte("{}")
	}

	query := `
		INSERT INTO sepa_payments (
			id, payment_attempt_id, msg_id, pmt_inf_id, end_to_end_id,
			iban, bic, creditor_name, sepa_status,
			submitted_at, accepted_at, settled_at,
			reject_reason_code, reject_reason_desc,
			last_report_id, last_report_at, response_data,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`

	_, err = s.pool.Exec(ctx, query,
		payment.ID,
		payment.PaymentAttemptID,
		payment.MsgID,
		payment.PmtInfID,
		payment.EndToEndID,
		payment.IBAN,
		nullableString(payment.BIC),
		nullableString(payment.CreditorName),
		payment.Status,
		payment.SubmittedAt,
		payment.AcceptedAt,
		payment.SettledAt,
		nullableString(payment.RejectReasonCode),
		nullableString(payment.RejectReasonDesc),
		nullableString(payment.LastReportID),
		payment.LastReportAt,
		responseData,
		payment.CreatedAt,
		payment.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert sepa payment: %w", err)
	}

	return nil
}

// GetByMsgAndPmtInf retrieves a SEPA payment by message and payment info IDs.
func (s *PostgresStore) GetByMsgAndPmtInf(ctx context.Context, msgID, pmtInfID string) (*SEPAPayment, error) {
	query := `
		SELECT id, payment_attempt_id, msg_id, pmt_inf_id, end_to_end_id,
			   iban, bic, creditor_name, sepa_status,
			   submitted_at, accepted_at, settled_at,
			   reject_reason_code, reject_reason_desc,
			   last_report_id, last_report_at, response_data,
			   created_at, updated_at
		FROM sepa_payments
		WHERE msg_id = $1 AND pmt_inf_id = $2
	`

	row := s.pool.QueryRow(ctx, query, msgID, pmtInfID)
	return s.scanPayment(row)
}

// GetByEndToEndID retrieves a SEPA payment by end-to-end ID.
func (s *PostgresStore) GetByEndToEndID(ctx context.Context, endToEndID string) (*SEPAPayment, error) {
	query := `
		SELECT id, payment_attempt_id, msg_id, pmt_inf_id, end_to_end_id,
			   iban, bic, creditor_name, sepa_status,
			   submitted_at, accepted_at, settled_at,
			   reject_reason_code, reject_reason_desc,
			   last_report_id, last_report_at, response_data,
			   created_at, updated_at
		FROM sepa_payments
		WHERE end_to_end_id = $1
	`

	row := s.pool.QueryRow(ctx, query, endToEndID)
	return s.scanPayment(row)
}

// UpdateStatus updates the SEPA payment status.
func (s *PostgresStore) UpdateStatus(ctx context.Context, msgID, pmtInfID string, status SEPAStatus, responseData map[string]any) error {
	respDataJSON, err := json.Marshal(responseData)
	if err != nil {
		respDataJSON = []byte("{}")
	}

	query := `
		UPDATE sepa_payments
		SET sepa_status = $3, response_data = $4
		WHERE msg_id = $1 AND pmt_inf_id = $2
	`

	result, err := s.pool.Exec(ctx, query, msgID, pmtInfID, status, respDataJSON)
	if err != nil {
		return fmt.Errorf("update sepa payment status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sepa payment not found: %s/%s", msgID, pmtInfID)
	}

	return nil
}

// MarkAccepted marks the SEPA payment as accepted.
func (s *PostgresStore) MarkAccepted(ctx context.Context, msgID, pmtInfID string, acceptedAt time.Time) error {
	query := `
		UPDATE sepa_payments
		SET sepa_status = $3, accepted_at = $4
		WHERE msg_id = $1 AND pmt_inf_id = $2
	`

	result, err := s.pool.Exec(ctx, query, msgID, pmtInfID, SEPAAccepted, acceptedAt)
	if err != nil {
		return fmt.Errorf("mark sepa payment accepted: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sepa payment not found: %s/%s", msgID, pmtInfID)
	}

	return nil
}

// MarkSettled marks the SEPA payment as settled.
func (s *PostgresStore) MarkSettled(ctx context.Context, msgID, pmtInfID string, settledAt time.Time) error {
	query := `
		UPDATE sepa_payments
		SET sepa_status = $3, settled_at = $4
		WHERE msg_id = $1 AND pmt_inf_id = $2
	`

	result, err := s.pool.Exec(ctx, query, msgID, pmtInfID, SEPASettled, settledAt)
	if err != nil {
		return fmt.Errorf("mark sepa payment settled: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sepa payment not found: %s/%s", msgID, pmtInfID)
	}

	return nil
}

// MarkRejected marks the SEPA payment as rejected.
func (s *PostgresStore) MarkRejected(ctx context.Context, msgID, pmtInfID string, reasonCode, reasonDesc string) error {
	query := `
		UPDATE sepa_payments
		SET sepa_status = $3, reject_reason_code = $4, reject_reason_desc = $5
		WHERE msg_id = $1 AND pmt_inf_id = $2
	`

	result, err := s.pool.Exec(ctx, query, msgID, pmtInfID, SEPARejected, reasonCode, reasonDesc)
	if err != nil {
		return fmt.Errorf("mark sepa payment rejected: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sepa payment not found: %s/%s", msgID, pmtInfID)
	}

	return nil
}

// GetPendingPayments retrieves SEPA payments in SUBMITTED or ACCEPTED status.
func (s *PostgresStore) GetPendingPayments(ctx context.Context, olderThan time.Duration, limit int) ([]*SEPAPayment, error) {
	cutoff := time.Now().Add(-olderThan)

	query := `
		SELECT id, payment_attempt_id, msg_id, pmt_inf_id, end_to_end_id,
			   iban, bic, creditor_name, sepa_status,
			   submitted_at, accepted_at, settled_at,
			   reject_reason_code, reject_reason_desc,
			   last_report_id, last_report_at, response_data,
			   created_at, updated_at
		FROM sepa_payments
		WHERE sepa_status IN ('SUBMITTED', 'ACCEPTED')
		  AND submitted_at < $1
		ORDER BY submitted_at ASC
		LIMIT $2
	`

	rows, err := s.pool.Query(ctx, query, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending sepa payments: %w", err)
	}
	defer rows.Close()

	var payments []*SEPAPayment
	for rows.Next() {
		payment, err := s.scanPaymentRow(rows)
		if err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}

	return payments, rows.Err()
}

// UpdateFromReport updates a payment based on report data.
func (s *PostgresStore) UpdateFromReport(ctx context.Context, msgID, pmtInfID, reportID string, status SEPAStatus, reasonCode, reasonDesc string) error {
	now := time.Now()

	var query string
	var args []any

	switch status {
	case SEPAAccepted:
		query = `
			UPDATE sepa_payments
			SET sepa_status = $3, accepted_at = $4, last_report_id = $5, last_report_at = $6
			WHERE msg_id = $1 AND pmt_inf_id = $2
		`
		args = []any{msgID, pmtInfID, status, now, reportID, now}
	case SEPASettled:
		query = `
			UPDATE sepa_payments
			SET sepa_status = $3, settled_at = $4, last_report_id = $5, last_report_at = $6
			WHERE msg_id = $1 AND pmt_inf_id = $2
		`
		args = []any{msgID, pmtInfID, status, now, reportID, now}
	case SEPARejected:
		query = `
			UPDATE sepa_payments
			SET sepa_status = $3, reject_reason_code = $4, reject_reason_desc = $5, last_report_id = $6, last_report_at = $7
			WHERE msg_id = $1 AND pmt_inf_id = $2
		`
		args = []any{msgID, pmtInfID, status, reasonCode, reasonDesc, reportID, now}
	default:
		return fmt.Errorf("unsupported status for report update: %s", status)
	}

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update sepa payment from report: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sepa payment not found: %s/%s", msgID, pmtInfID)
	}

	return nil
}

func (s *PostgresStore) scanPayment(row pgx.Row) (*SEPAPayment, error) {
	var payment SEPAPayment
	var bic, creditorName, rejectCode, rejectDesc, lastReportID *string
	var responseDataJSON []byte

	err := row.Scan(
		&payment.ID,
		&payment.PaymentAttemptID,
		&payment.MsgID,
		&payment.PmtInfID,
		&payment.EndToEndID,
		&payment.IBAN,
		&bic,
		&creditorName,
		&payment.Status,
		&payment.SubmittedAt,
		&payment.AcceptedAt,
		&payment.SettledAt,
		&rejectCode,
		&rejectDesc,
		&lastReportID,
		&payment.LastReportAt,
		&responseDataJSON,
		&payment.CreatedAt,
		&payment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("sepa payment not found")
		}
		return nil, fmt.Errorf("scan sepa payment: %w", err)
	}

	if bic != nil {
		payment.BIC = *bic
	}
	if creditorName != nil {
		payment.CreditorName = *creditorName
	}
	if rejectCode != nil {
		payment.RejectReasonCode = *rejectCode
	}
	if rejectDesc != nil {
		payment.RejectReasonDesc = *rejectDesc
	}
	if lastReportID != nil {
		payment.LastReportID = *lastReportID
	}

	if len(responseDataJSON) > 0 {
		json.Unmarshal(responseDataJSON, &payment.ResponseData)
	}

	return &payment, nil
}

func (s *PostgresStore) scanPaymentRow(rows pgx.Rows) (*SEPAPayment, error) {
	var payment SEPAPayment
	var bic, creditorName, rejectCode, rejectDesc, lastReportID *string
	var responseDataJSON []byte

	err := rows.Scan(
		&payment.ID,
		&payment.PaymentAttemptID,
		&payment.MsgID,
		&payment.PmtInfID,
		&payment.EndToEndID,
		&payment.IBAN,
		&bic,
		&creditorName,
		&payment.Status,
		&payment.SubmittedAt,
		&payment.AcceptedAt,
		&payment.SettledAt,
		&rejectCode,
		&rejectDesc,
		&lastReportID,
		&payment.LastReportAt,
		&responseDataJSON,
		&payment.CreatedAt,
		&payment.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan sepa payment row: %w", err)
	}

	if bic != nil {
		payment.BIC = *bic
	}
	if creditorName != nil {
		payment.CreditorName = *creditorName
	}
	if rejectCode != nil {
		payment.RejectReasonCode = *rejectCode
	}
	if rejectDesc != nil {
		payment.RejectReasonDesc = *rejectDesc
	}
	if lastReportID != nil {
		payment.LastReportID = *lastReportID
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
