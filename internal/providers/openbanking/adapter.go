// Package openbanking provides Open Banking payment initiation (UK and EU PSD2).
package openbanking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"finplatform/internal/domain"
	"finplatform/internal/events"
)

// Scheme represents the Open Banking scheme.
type Scheme string

const (
	SchemeUK        Scheme = "UK"
	SchemeEUSEPA    Scheme = "EU_SEPA"
	SchemeEUInstant Scheme = "EU_INSTANT"
)

// Status represents the payment status.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusAuthorised Status = "AUTHORISED"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

// Config holds Open Banking adapter configuration.
type Config struct {
	BaseURL     string        `env:"OB_BASE_URL"`
	ClientID    string        `env:"OB_CLIENT_ID"`
	ClientSecret string       `env:"OB_CLIENT_SECRET"`
	RedirectURL string        `env:"OB_REDIRECT_URL"`
	Timeout     time.Duration `env:"OB_TIMEOUT" envDefault:"30s"`
}

// Payment represents an Open Banking payment.
type Payment struct {
	ID           string
	TenantID     domain.TenantID
	CustomerID   domain.CustomerID
	PaymentID    string // OB provider payment ID
	ConsentID    string
	Scheme       Scheme
	AmountMinor  int64
	Currency     domain.Currency
	DebtorIBAN   string
	DebtorName   string
	Reference    string
	Status       Status
	DepositID    *domain.DepositID
	InitiatedAt  time.Time
	AuthorisedAt *time.Time
	CompletedAt  *time.Time
	ErrorCode    string
	ErrorMessage string
	ResponseData map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// InitiateRequest is the request to initiate an Open Banking payment.
type InitiateRequest struct {
	TenantID    domain.TenantID
	CustomerID  domain.CustomerID
	AmountMinor int64
	Currency    domain.Currency
	Scheme      Scheme
	Reference   string
	RedirectURL string
}

// InitiateResponse is the response from payment initiation.
type InitiateResponse struct {
	PaymentID   string `json:"payment_id"`
	ConsentID   string `json:"consent_id"`
	AuthURL     string `json:"auth_url"` // Redirect user here for bank authorization
	Status      string `json:"status"`
}

// Adapter implements the Open Banking payment provider.
type Adapter struct {
	config     Config
	httpClient *http.Client
	store      *Store
	publisher  EventPublisher
	logger     *slog.Logger
}

// EventPublisher publishes events.
type EventPublisher interface {
	Publish(ctx context.Context, subject string, env *events.Envelope) error
}

// NewAdapter creates a new Open Banking adapter.
func NewAdapter(cfg Config, store *Store, publisher EventPublisher, logger *slog.Logger) *Adapter {
	return &Adapter{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		store:     store,
		publisher: publisher,
		logger:    logger,
	}
}

// Initiate starts an Open Banking payment flow.
// Returns the auth URL where the user should be redirected.
func (a *Adapter) Initiate(ctx context.Context, req *InitiateRequest) (*InitiateResponse, error) {
	a.logger.Info("initiating Open Banking payment",
		"customer_id", req.CustomerID,
		"amount", req.AmountMinor,
		"scheme", req.Scheme,
	)

	// Call OB provider to create payment
	apiReq := map[string]any{
		"amount":       float64(req.AmountMinor) / 100,
		"currency":     req.Currency,
		"scheme":       req.Scheme,
		"reference":    req.Reference,
		"redirect_url": req.RedirectURL,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.BaseURL+"/payments/initiate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.getAccessToken(ctx))

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(httpResp.Body)

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("ob api error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp InitiateResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Store payment record
	payment := &Payment{
		ID:          ulid.Make().String(),
		TenantID:    req.TenantID,
		CustomerID:  req.CustomerID,
		PaymentID:   resp.PaymentID,
		ConsentID:   resp.ConsentID,
		Scheme:      req.Scheme,
		AmountMinor: req.AmountMinor,
		Currency:    req.Currency,
		Reference:   req.Reference,
		Status:      StatusPending,
		InitiatedAt: time.Now(),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := a.store.Create(ctx, payment); err != nil {
		return nil, fmt.Errorf("store payment: %w", err)
	}

	a.logger.Info("Open Banking payment initiated",
		"payment_id", resp.PaymentID,
		"auth_url", resp.AuthURL,
	)

	return &resp, nil
}

// HandleCallback processes the callback after user authorization.
func (a *Adapter) HandleCallback(ctx context.Context, paymentID string) error {
	payment, err := a.store.GetByPaymentID(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("get payment: %w", err)
	}

	// Check status with OB provider
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.config.BaseURL+"/payments/"+paymentID, nil)
	httpReq.Header.Set("Authorization", "Bearer "+a.getAccessToken(ctx))

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("check status: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(httpResp.Body)

	var status struct {
		Status       string `json:"status"`
		DebtorIBAN   string `json:"debtor_iban"`
		DebtorName   string `json:"debtor_name"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	json.Unmarshal(respBody, &status)

	switch status.Status {
	case "AUTHORISED":
		now := time.Now()
		payment.Status = StatusAuthorised
		payment.AuthorisedAt = &now
		payment.DebtorIBAN = status.DebtorIBAN
		payment.DebtorName = status.DebtorName
		a.store.UpdateAuthorised(ctx, paymentID, status.DebtorIBAN, status.DebtorName)

	case "COMPLETED":
		now := time.Now()
		payment.Status = StatusCompleted
		payment.CompletedAt = &now
		a.store.UpdateCompleted(ctx, paymentID)

		// Publish deposit event
		a.publishDepositDetected(ctx, payment)

	case "FAILED":
		payment.Status = StatusFailed
		payment.ErrorCode = status.ErrorCode
		payment.ErrorMessage = status.ErrorMessage
		a.store.UpdateFailed(ctx, paymentID, status.ErrorCode, status.ErrorMessage)
	}

	return nil
}

func (a *Adapter) publishDepositDetected(ctx context.Context, payment *Payment) {
	if a.publisher == nil {
		return
	}

	depositID := domain.DepositID(ulid.Make().String())

	// Link deposit to OB payment
	a.store.LinkDeposit(ctx, payment.PaymentID, depositID)

	event := events.DepositInboundDetected{
		DepositID:   depositID,
		Rail:        domain.Rail("OPENBANKING_" + string(payment.Scheme)),
		AmountMinor: payment.AmountMinor,
		Currency:    payment.Currency,
		ExternalRef: payment.PaymentID,
		ReceivedAt:  time.Now(),
	}

	env, _ := events.NewEnvelope("deposit.inbound.detected.v1", payment.TenantID, payment.PaymentID, &event)
	a.publisher.Publish(ctx, events.SubjectDepositInboundDetected, env)
}

func (a *Adapter) getAccessToken(ctx context.Context) string {
	// Simplified - real implementation would use OAuth2 client credentials flow
	return a.config.ClientSecret
}

// Store handles Open Banking payment persistence.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new Open Banking store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new payment record.
func (s *Store) Create(ctx context.Context, payment *Payment) error {
	query := `
		INSERT INTO openbanking_payments (
			id, tenant_id, customer_id, payment_id, consent_id, scheme,
			amount_minor, currency, debtor_iban, debtor_name, reference,
			ob_status, deposit_id, initiated_at, authorised_at, completed_at,
			error_code, error_message, response_data, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`

	responseData, _ := json.Marshal(payment.ResponseData)

	_, err := s.pool.Exec(ctx, query,
		payment.ID, payment.TenantID, payment.CustomerID,
		payment.PaymentID, payment.ConsentID, payment.Scheme,
		payment.AmountMinor, payment.Currency,
		nullableString(payment.DebtorIBAN), nullableString(payment.DebtorName),
		nullableString(payment.Reference),
		payment.Status, payment.DepositID,
		payment.InitiatedAt, payment.AuthorisedAt, payment.CompletedAt,
		nullableString(payment.ErrorCode), nullableString(payment.ErrorMessage),
		responseData, payment.CreatedAt, payment.UpdatedAt,
	)
	return err
}

// GetByPaymentID retrieves a payment by OB payment ID.
func (s *Store) GetByPaymentID(ctx context.Context, paymentID string) (*Payment, error) {
	query := `
		SELECT id, tenant_id, customer_id, payment_id, consent_id, scheme,
			   amount_minor, currency, debtor_iban, debtor_name, reference,
			   ob_status, deposit_id, initiated_at, authorised_at, completed_at,
			   error_code, error_message, response_data, created_at, updated_at
		FROM openbanking_payments WHERE payment_id = $1
	`

	row := s.pool.QueryRow(ctx, query, paymentID)

	var p Payment
	var consentID, debtorIBAN, debtorName, reference, errorCode, errorMsg *string
	var depositID *string
	var responseData []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &p.CustomerID, &p.PaymentID, &consentID, &p.Scheme,
		&p.AmountMinor, &p.Currency, &debtorIBAN, &debtorName, &reference,
		&p.Status, &depositID, &p.InitiatedAt, &p.AuthorisedAt, &p.CompletedAt,
		&errorCode, &errorMsg, &responseData, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("payment not found: %s", paymentID)
		}
		return nil, err
	}

	if consentID != nil {
		p.ConsentID = *consentID
	}
	if debtorIBAN != nil {
		p.DebtorIBAN = *debtorIBAN
	}
	if debtorName != nil {
		p.DebtorName = *debtorName
	}
	if reference != nil {
		p.Reference = *reference
	}
	if errorCode != nil {
		p.ErrorCode = *errorCode
	}
	if errorMsg != nil {
		p.ErrorMessage = *errorMsg
	}
	if depositID != nil {
		d := domain.DepositID(*depositID)
		p.DepositID = &d
	}

	return &p, nil
}

// UpdateAuthorised marks payment as authorised.
func (s *Store) UpdateAuthorised(ctx context.Context, paymentID, debtorIBAN, debtorName string) error {
	query := `
		UPDATE openbanking_payments
		SET ob_status = $2, authorised_at = $3, debtor_iban = $4, debtor_name = $5
		WHERE payment_id = $1
	`
	_, err := s.pool.Exec(ctx, query, paymentID, StatusAuthorised, time.Now(), debtorIBAN, debtorName)
	return err
}

// UpdateCompleted marks payment as completed.
func (s *Store) UpdateCompleted(ctx context.Context, paymentID string) error {
	query := `UPDATE openbanking_payments SET ob_status = $2, completed_at = $3 WHERE payment_id = $1`
	_, err := s.pool.Exec(ctx, query, paymentID, StatusCompleted, time.Now())
	return err
}

// UpdateFailed marks payment as failed.
func (s *Store) UpdateFailed(ctx context.Context, paymentID, errorCode, errorMsg string) error {
	query := `UPDATE openbanking_payments SET ob_status = $2, error_code = $3, error_message = $4 WHERE payment_id = $1`
	_, err := s.pool.Exec(ctx, query, paymentID, StatusFailed, errorCode, errorMsg)
	return err
}

// LinkDeposit links a deposit to the OB payment.
func (s *Store) LinkDeposit(ctx context.Context, paymentID string, depositID domain.DepositID) error {
	query := `UPDATE openbanking_payments SET deposit_id = $2 WHERE payment_id = $1`
	_, err := s.pool.Exec(ctx, query, paymentID, depositID)
	return err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
