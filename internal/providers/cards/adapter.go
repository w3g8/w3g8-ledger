// Package cards provides card payment processing via w3g8 acquiring.
package cards

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

// CardBrand represents the card brand.
type CardBrand string

const (
	BrandVisa       CardBrand = "VISA"
	BrandMastercard CardBrand = "MASTERCARD"
	BrandAmex       CardBrand = "AMEX"
)

// CardType represents the card type.
type CardType string

const (
	TypeDebit   CardType = "DEBIT"
	TypeCredit  CardType = "CREDIT"
	TypePrepaid CardType = "PREPAID"
)

// Status represents the payment status.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusAuthorised Status = "AUTHORISED"
	StatusCaptured   Status = "CAPTURED"
	StatusFailed     Status = "FAILED"
	StatusRefunded   Status = "REFUNDED"
)

// Config holds card adapter configuration.
type Config struct {
	BaseURL     string        `env:"CARDS_BASE_URL"`
	MerchantID  string        `env:"CARDS_MERCHANT_ID"`
	APIKey      string        `env:"CARDS_API_KEY"`
	Timeout     time.Duration `env:"CARDS_TIMEOUT" envDefault:"30s"`
	AutoCapture bool          `env:"CARDS_AUTO_CAPTURE" envDefault:"true"`
}

// Payment represents a card payment.
type Payment struct {
	ID             string
	TenantID       domain.TenantID
	CustomerID     domain.CustomerID
	CardToken      string
	TransactionID  string
	AuthCode       string
	CardLastFour   string
	CardBrand      CardBrand
	CardType       CardType
	AmountMinor    int64
	Currency       domain.Currency
	ThreeDSVersion string
	ThreeDSStatus  string
	Status         Status
	DepositID      *domain.DepositID
	InitiatedAt    time.Time
	AuthorisedAt   *time.Time
	CapturedAt     *time.Time
	ErrorCode      string
	ErrorMessage   string
	DeclineReason  string
	ResponseData   map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ChargeRequest is the request to charge a card.
type ChargeRequest struct {
	TenantID    domain.TenantID
	CustomerID  domain.CustomerID
	CardToken   string // Tokenized card from vault
	AmountMinor int64
	Currency    domain.Currency
	Reference   string
	ThreeDS     *ThreeDSData
}

// ThreeDSData contains 3D Secure authentication data.
type ThreeDSData struct {
	Version      string
	Cavv         string
	Eci          string
	TransactionID string
}

// ChargeResponse is the response from a charge attempt.
type ChargeResponse struct {
	TransactionID  string    `json:"transaction_id"`
	AuthCode       string    `json:"auth_code"`
	Status         string    `json:"status"` // AUTHORISED, CAPTURED, FAILED
	CardLastFour   string    `json:"card_last_four"`
	CardBrand      string    `json:"card_brand"`
	CardType       string    `json:"card_type"`
	ErrorCode      string    `json:"error_code,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	DeclineReason  string    `json:"decline_reason,omitempty"`
}

// Adapter implements the card payment provider.
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

// NewAdapter creates a new card adapter.
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

// Charge processes a card payment.
func (a *Adapter) Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error) {
	a.logger.Info("charging card",
		"customer_id", req.CustomerID,
		"amount", req.AmountMinor,
		"card_token", maskToken(req.CardToken),
	)

	// Build API request
	apiReq := map[string]any{
		"merchant_id": a.config.MerchantID,
		"card_token":  req.CardToken,
		"amount":      float64(req.AmountMinor) / 100,
		"currency":    req.Currency,
		"reference":   req.Reference,
		"capture":     a.config.AutoCapture,
	}

	if req.ThreeDS != nil {
		apiReq["three_ds"] = map[string]any{
			"version":        req.ThreeDS.Version,
			"cavv":           req.ThreeDS.Cavv,
			"eci":            req.ThreeDS.Eci,
			"transaction_id": req.ThreeDS.TransactionID,
		}
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.BaseURL+"/charge", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(httpResp.Body)

	var resp ChargeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Store payment record
	payment := &Payment{
		ID:            ulid.Make().String(),
		TenantID:      req.TenantID,
		CustomerID:    req.CustomerID,
		CardToken:     req.CardToken,
		TransactionID: resp.TransactionID,
		AuthCode:      resp.AuthCode,
		CardLastFour:  resp.CardLastFour,
		CardBrand:     CardBrand(resp.CardBrand),
		CardType:      CardType(resp.CardType),
		AmountMinor:   req.AmountMinor,
		Currency:      req.Currency,
		InitiatedAt:   time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if req.ThreeDS != nil {
		payment.ThreeDSVersion = req.ThreeDS.Version
		payment.ThreeDSStatus = "AUTHENTICATED"
	}

	switch resp.Status {
	case "AUTHORISED":
		payment.Status = StatusAuthorised
		now := time.Now()
		payment.AuthorisedAt = &now
	case "CAPTURED":
		payment.Status = StatusCaptured
		now := time.Now()
		payment.AuthorisedAt = &now
		payment.CapturedAt = &now
		// Publish deposit event for captured payments
		a.publishDepositDetected(ctx, payment)
	case "FAILED":
		payment.Status = StatusFailed
		payment.ErrorCode = resp.ErrorCode
		payment.ErrorMessage = resp.ErrorMessage
		payment.DeclineReason = resp.DeclineReason
	}

	if err := a.store.Create(ctx, payment); err != nil {
		a.logger.Error("failed to store payment", "error", err)
	}

	a.logger.Info("card charge completed",
		"transaction_id", resp.TransactionID,
		"status", resp.Status,
	)

	return &resp, nil
}

// Capture captures a previously authorized payment.
func (a *Adapter) Capture(ctx context.Context, transactionID string) error {
	payment, err := a.store.GetByTransactionID(ctx, transactionID)
	if err != nil {
		return err
	}

	if payment.Status != StatusAuthorised {
		return fmt.Errorf("payment not in AUTHORISED status: %s", payment.Status)
	}

	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		a.config.BaseURL+"/capture/"+transactionID, nil)
	httpReq.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("capture request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		return fmt.Errorf("capture failed: status=%d", httpResp.StatusCode)
	}

	// Update status
	a.store.MarkCaptured(ctx, transactionID)

	// Publish deposit event
	payment.Status = StatusCaptured
	now := time.Now()
	payment.CapturedAt = &now
	a.publishDepositDetected(ctx, payment)

	return nil
}

// Refund refunds a captured payment.
func (a *Adapter) Refund(ctx context.Context, transactionID string, amountMinor int64) error {
	payment, err := a.store.GetByTransactionID(ctx, transactionID)
	if err != nil {
		return err
	}

	if payment.Status != StatusCaptured {
		return fmt.Errorf("payment not in CAPTURED status: %s", payment.Status)
	}

	apiReq := map[string]any{
		"amount": float64(amountMinor) / 100,
	}
	body, _ := json.Marshal(apiReq)

	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		a.config.BaseURL+"/refund/"+transactionID, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("refund request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		return fmt.Errorf("refund failed: status=%d", httpResp.StatusCode)
	}

	// Update status (could be partial refund, but simplified here)
	a.store.MarkRefunded(ctx, transactionID)

	return nil
}

func (a *Adapter) publishDepositDetected(ctx context.Context, payment *Payment) {
	if a.publisher == nil {
		return
	}

	depositID := domain.DepositID(ulid.Make().String())

	// Link deposit to card payment
	a.store.LinkDeposit(ctx, payment.TransactionID, depositID)

	event := events.DepositInboundDetected{
		DepositID:   depositID,
		Rail:        domain.Rail("CARD_" + string(payment.CardBrand)),
		AmountMinor: payment.AmountMinor,
		Currency:    payment.Currency,
		ExternalRef: payment.TransactionID,
		ReceivedAt:  time.Now(),
	}

	env, _ := events.NewEnvelope("deposit.inbound.detected.v1", payment.TenantID, payment.TransactionID, &event)
	a.publisher.Publish(ctx, events.SubjectDepositInboundDetected, env)
}

func maskToken(token string) string {
	if len(token) > 8 {
		return token[:4] + "****" + token[len(token)-4:]
	}
	return "****"
}

// Store handles card payment persistence.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new card store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new payment record.
func (s *Store) Create(ctx context.Context, payment *Payment) error {
	query := `
		INSERT INTO card_payments (
			id, tenant_id, customer_id, card_token, transaction_id, auth_code,
			card_last_four, card_brand, card_type, amount_minor, currency,
			three_ds_version, three_ds_status, card_status, deposit_id,
			initiated_at, authorised_at, captured_at,
			error_code, error_message, decline_reason, response_data,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
	`

	responseData, _ := json.Marshal(payment.ResponseData)

	_, err := s.pool.Exec(ctx, query,
		payment.ID, payment.TenantID, payment.CustomerID,
		payment.CardToken, payment.TransactionID, nullableString(payment.AuthCode),
		nullableString(payment.CardLastFour), payment.CardBrand, payment.CardType,
		payment.AmountMinor, payment.Currency,
		nullableString(payment.ThreeDSVersion), nullableString(payment.ThreeDSStatus),
		payment.Status, payment.DepositID,
		payment.InitiatedAt, payment.AuthorisedAt, payment.CapturedAt,
		nullableString(payment.ErrorCode), nullableString(payment.ErrorMessage),
		nullableString(payment.DeclineReason), responseData,
		payment.CreatedAt, payment.UpdatedAt,
	)
	return err
}

// GetByTransactionID retrieves a payment by transaction ID.
func (s *Store) GetByTransactionID(ctx context.Context, txnID string) (*Payment, error) {
	query := `
		SELECT id, tenant_id, customer_id, card_token, transaction_id, auth_code,
			   card_last_four, card_brand, card_type, amount_minor, currency,
			   three_ds_version, three_ds_status, card_status, deposit_id,
			   initiated_at, authorised_at, captured_at,
			   error_code, error_message, decline_reason, response_data,
			   created_at, updated_at
		FROM card_payments WHERE transaction_id = $1
	`

	row := s.pool.QueryRow(ctx, query, txnID)

	var p Payment
	var authCode, lastFour, threeDSVer, threeDSStatus *string
	var errorCode, errorMsg, declineReason *string
	var depositID *string
	var responseData []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &p.CustomerID,
		&p.CardToken, &p.TransactionID, &authCode,
		&lastFour, &p.CardBrand, &p.CardType,
		&p.AmountMinor, &p.Currency,
		&threeDSVer, &threeDSStatus, &p.Status, &depositID,
		&p.InitiatedAt, &p.AuthorisedAt, &p.CapturedAt,
		&errorCode, &errorMsg, &declineReason, &responseData,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("payment not found: %s", txnID)
		}
		return nil, err
	}

	if authCode != nil {
		p.AuthCode = *authCode
	}
	if lastFour != nil {
		p.CardLastFour = *lastFour
	}
	if threeDSVer != nil {
		p.ThreeDSVersion = *threeDSVer
	}
	if threeDSStatus != nil {
		p.ThreeDSStatus = *threeDSStatus
	}
	if errorCode != nil {
		p.ErrorCode = *errorCode
	}
	if errorMsg != nil {
		p.ErrorMessage = *errorMsg
	}
	if declineReason != nil {
		p.DeclineReason = *declineReason
	}
	if depositID != nil {
		d := domain.DepositID(*depositID)
		p.DepositID = &d
	}

	return &p, nil
}

// MarkCaptured marks payment as captured.
func (s *Store) MarkCaptured(ctx context.Context, txnID string) error {
	query := `UPDATE card_payments SET card_status = $2, captured_at = $3 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, StatusCaptured, time.Now())
	return err
}

// MarkRefunded marks payment as refunded.
func (s *Store) MarkRefunded(ctx context.Context, txnID string) error {
	query := `UPDATE card_payments SET card_status = $2 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, StatusRefunded)
	return err
}

// LinkDeposit links a deposit to the card payment.
func (s *Store) LinkDeposit(ctx context.Context, txnID string, depositID domain.DepositID) error {
	query := `UPDATE card_payments SET deposit_id = $2 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, depositID)
	return err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
