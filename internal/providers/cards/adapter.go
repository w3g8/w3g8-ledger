// Package cards provides card payment processing via w3g8-card-payments acquiring.
package cards

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/oklog/ulid/v2"

	"finplatform/internal/common/money"
	"finplatform/internal/funding"
)

// NATS subjects for acquiring service.
const (
	SubjectAuthorize = "acquiring.authorize"
	SubjectCapture   = "acquiring.capture"
	SubjectVoid      = "acquiring.void"
	SubjectRefund    = "acquiring.refund"

	// Event subjects from acquiring.
	SubjectTxnApproved   = "acquiring.events.txn.approved"
	SubjectTxnDeclined   = "acquiring.events.txn.declined"
	SubjectTxnCaptured   = "acquiring.events.txn.captured"
	SubjectTxnRefunded   = "acquiring.events.txn.refunded"
	SubjectTxnVoided     = "acquiring.events.txn.voided"
	SubjectTxnChargeback = "acquiring.events.txn.chargeback"
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
	StatusVoided     Status = "VOIDED"
	StatusChargeback Status = "CHARGEBACK"
)

// Config holds card adapter configuration.
type Config struct {
	NATSUrl        string        `env:"NATS_URL"`
	MerchantID     string        `env:"CARDS_MERCHANT_ID"`
	RequestTimeout time.Duration `env:"CARDS_TIMEOUT" envDefault:"30s"`
	AutoCapture    bool          `env:"CARDS_AUTO_CAPTURE" envDefault:"true"`
}

// Payment represents a card payment.
type Payment struct {
	ID             string
	TenantID       string
	WalletID       string
	CustomerID     string
	IntentID       string // Links to FundingIntent
	CardToken      string
	TransactionID  string
	AuthCode       string
	CardLastFour   string
	CardBrand      CardBrand
	CardType       CardType
	AmountMinor    int64
	Currency       string
	ThreeDSVersion string
	ThreeDSStatus  string
	Status         Status
	InitiatedAt    time.Time
	AuthorisedAt   *time.Time
	CapturedAt     *time.Time
	RefundedAt     *time.Time
	ChargebackAt   *time.Time
	ErrorCode      string
	ErrorMessage   string
	DeclineReason  string
	ResponseData   map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AuthorizeRequest is sent to acquiring service.
type AuthorizeRequest struct {
	TransactionID string         `json:"transactionId"`
	MerchantID    string         `json:"merchantId"`
	Amount        int64          `json:"amount"`
	Currency      string         `json:"currency"`
	CardToken     string         `json:"cardToken"`
	ThreeDS       *ThreeDSData   `json:"threeDs,omitempty"`
	EntryMode     string         `json:"entryMode"`
	Capture       bool           `json:"capture"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// AuthorizeResponse from acquiring service.
type AuthorizeResponse struct {
	Success         bool   `json:"success"`
	TransactionID   string `json:"transactionId"`
	Approved        bool   `json:"approved"`
	AuthCode        string `json:"authCode"`
	ResponseCode    string `json:"responseCode"`
	ResponseMessage string `json:"responseMessage"`
	CardBrand       string `json:"cardBrand,omitempty"`
	CardLastFour    string `json:"cardLast4,omitempty"`
	Error           string `json:"error,omitempty"`
	Message         string `json:"message,omitempty"`
}

// CaptureRequest is sent to acquiring for capture.
type CaptureRequest struct {
	TransactionID string `json:"transactionId"`
	Amount        int64  `json:"amount,omitempty"`
}

// RefundRequest is sent to acquiring for refund.
type RefundRequest struct {
	TransactionID string `json:"transactionId"`
	Amount        int64  `json:"amount"`
	Reason        string `json:"reason,omitempty"`
}

// ThreeDSData contains 3D Secure authentication data.
type ThreeDSData struct {
	Version       string `json:"version"`
	Cavv          string `json:"cavv"`
	Eci           string `json:"eci"`
	TransactionID string `json:"dsTransactionId,omitempty"`
	Status        string `json:"status"`
}

// ChargebackEvent from acquiring service.
type ChargebackEvent struct {
	TransactionID   string    `json:"transactionId"`
	ChargebackID    string    `json:"chargebackId"`
	Amount          int64     `json:"amount"`
	Reason          string    `json:"reason"`
	ReasonCode      string    `json:"reasonCode"`
	Timestamp       time.Time `json:"timestamp"`
	NetworkRef      string    `json:"networkRef,omitempty"`
	AcquirerRef     string    `json:"acquirerRef,omitempty"`
	ResponseDueDate time.Time `json:"responseDueDate,omitempty"`
}

// FundingService callback interface.
type FundingService interface {
	ProcessCardPayment(ctx context.Context, intentID, transactionID string, captured bool) error
	ProcessChargeback(ctx context.Context, intentID, reason string) error
}

// Adapter implements the card payment provider.
type Adapter struct {
	config         Config
	nc             *nats.Conn
	store          *Store
	fundingService FundingService
	logger         *slog.Logger
	subs           []*nats.Subscription
}

// NewAdapter creates a new card adapter.
func NewAdapter(cfg Config, nc *nats.Conn, store *Store, fundingSvc FundingService, logger *slog.Logger) (*Adapter, error) {
	a := &Adapter{
		config:         cfg,
		nc:             nc,
		store:          store,
		fundingService: fundingSvc,
		logger:         logger,
	}

	// Subscribe to acquiring events
	if err := a.subscribeToEvents(); err != nil {
		return nil, fmt.Errorf("subscribe to events: %w", err)
	}

	return a, nil
}

// subscribeToEvents subscribes to acquiring event subjects.
func (a *Adapter) subscribeToEvents() error {
	subjects := map[string]nats.MsgHandler{
		SubjectTxnCaptured:   a.handleCaptured,
		SubjectTxnRefunded:   a.handleRefunded,
		SubjectTxnChargeback: a.handleChargeback,
	}

	for subject, handler := range subjects {
		sub, err := a.nc.Subscribe(subject, handler)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
		a.subs = append(a.subs, sub)
		a.logger.Info("subscribed to acquiring events", "subject", subject)
	}

	return nil
}

// Close cleans up subscriptions.
func (a *Adapter) Close() {
	for _, sub := range a.subs {
		sub.Unsubscribe()
	}
}

// Charge implements CardProvider.Charge - authorizes and optionally captures a card payment.
func (a *Adapter) Charge(ctx context.Context, intent *funding.FundingIntent, cardToken string, threeDS *funding.ThreeDSData) (providerRef string, err error) {
	txnID := fmt.Sprintf("TXN-%s", ulid.Make().String())

	a.logger.Info("charging card",
		"intent_id", intent.ID,
		"transaction_id", txnID,
		"amount", intent.Amount.AmountMinor,
		"card_token", maskToken(cardToken),
	)

	// Build authorize request
	req := AuthorizeRequest{
		TransactionID: txnID,
		MerchantID:    a.config.MerchantID,
		Amount:        intent.Amount.AmountMinor,
		Currency:      string(intent.Amount.Currency),
		CardToken:     cardToken,
		EntryMode:     "ECOMMERCE",
		Capture:       a.config.AutoCapture,
		Metadata: map[string]any{
			"intent_id":   intent.ID,
			"wallet_id":   intent.WalletID,
			"customer_id": intent.CustomerID,
		},
	}

	if threeDS != nil {
		req.ThreeDS = &ThreeDSData{
			Version:       threeDS.Version,
			Cavv:          threeDS.Cavv,
			Eci:           threeDS.Eci,
			TransactionID: threeDS.TransactionID,
			Status:        "Y", // Authenticated
		}
	}

	reqData, _ := json.Marshal(req)

	// Send to acquiring via NATS request-reply
	msg, err := a.nc.RequestWithContext(ctx, SubjectAuthorize, reqData)
	if err != nil {
		return "", fmt.Errorf("nats request: %w", err)
	}

	var resp AuthorizeResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	// Create local payment record
	payment := &Payment{
		ID:            ulid.Make().String(),
		TenantID:      intent.TenantID,
		WalletID:      intent.WalletID,
		CustomerID:    intent.CustomerID,
		IntentID:      intent.ID,
		CardToken:     cardToken,
		TransactionID: txnID,
		AmountMinor:   intent.Amount.AmountMinor,
		Currency:      string(intent.Amount.Currency),
		Status:        StatusPending,
		InitiatedAt:   time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if threeDS != nil {
		payment.ThreeDSVersion = threeDS.Version
		payment.ThreeDSStatus = "AUTHENTICATED"
	}

	if !resp.Success || !resp.Approved {
		payment.Status = StatusFailed
		payment.ErrorCode = resp.ResponseCode
		payment.ErrorMessage = resp.ResponseMessage
		if resp.Error != "" {
			payment.ErrorCode = resp.Error
			payment.ErrorMessage = resp.Message
		}
		if err := a.store.Create(ctx, payment); err != nil {
			a.logger.Error("failed to store failed payment", "error", err)
		}
		return "", fmt.Errorf("authorization declined: %s - %s", resp.ResponseCode, resp.ResponseMessage)
	}

	// Authorization approved
	payment.AuthCode = resp.AuthCode
	payment.CardBrand = CardBrand(resp.CardBrand)
	payment.CardLastFour = resp.CardLastFour
	now := time.Now()
	payment.AuthorisedAt = &now

	if a.config.AutoCapture {
		payment.Status = StatusCaptured
		payment.CapturedAt = &now
	} else {
		payment.Status = StatusAuthorised
	}

	if err := a.store.Create(ctx, payment); err != nil {
		a.logger.Error("failed to store payment", "error", err)
	}

	a.logger.Info("card charge completed",
		"intent_id", intent.ID,
		"transaction_id", txnID,
		"auth_code", resp.AuthCode,
		"status", payment.Status,
	)

	return txnID, nil
}

// Capture implements CardProvider.Capture - captures a previously authorized payment.
func (a *Adapter) Capture(ctx context.Context, providerRef string) error {
	payment, err := a.store.GetByTransactionID(ctx, providerRef)
	if err != nil {
		return fmt.Errorf("get payment: %w", err)
	}

	if payment.Status != StatusAuthorised {
		return fmt.Errorf("payment not in AUTHORISED status: %s", payment.Status)
	}

	a.logger.Info("capturing payment", "transaction_id", providerRef)

	req := CaptureRequest{
		TransactionID: providerRef,
		Amount:        payment.AmountMinor,
	}
	reqData, _ := json.Marshal(req)

	msg, err := a.nc.RequestWithContext(ctx, SubjectCapture, reqData)
	if err != nil {
		return fmt.Errorf("nats capture request: %w", err)
	}

	var resp struct {
		Success       bool   `json:"success"`
		TransactionID string `json:"transactionId"`
		Status        string `json:"status"`
		Error         string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("unmarshal capture response: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("capture failed: %s", resp.Error)
	}

	// Update local status
	if err := a.store.MarkCaptured(ctx, providerRef); err != nil {
		a.logger.Error("failed to update capture status", "error", err)
	}

	a.logger.Info("payment captured", "transaction_id", providerRef)

	return nil
}

// Refund implements CardProvider.Refund - refunds a captured payment.
func (a *Adapter) Refund(ctx context.Context, providerRef string, amount money.Money) error {
	payment, err := a.store.GetByTransactionID(ctx, providerRef)
	if err != nil {
		return fmt.Errorf("get payment: %w", err)
	}

	if payment.Status != StatusCaptured {
		return fmt.Errorf("payment not in CAPTURED status: %s", payment.Status)
	}

	a.logger.Info("refunding payment",
		"transaction_id", providerRef,
		"amount", amount.AmountMinor,
	)

	req := RefundRequest{
		TransactionID: providerRef,
		Amount:        amount.AmountMinor,
		Reason:        "Customer requested refund",
	}
	reqData, _ := json.Marshal(req)

	msg, err := a.nc.RequestWithContext(ctx, SubjectRefund, reqData)
	if err != nil {
		return fmt.Errorf("nats refund request: %w", err)
	}

	var resp struct {
		Success             bool   `json:"success"`
		TransactionID       string `json:"transactionId"`
		RefundTransactionID string `json:"refundTransactionId"`
		Status              string `json:"status"`
		Error               string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("unmarshal refund response: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("refund failed: %s", resp.Error)
	}

	// Update local status
	if err := a.store.MarkRefunded(ctx, providerRef); err != nil {
		a.logger.Error("failed to update refund status", "error", err)
	}

	a.logger.Info("payment refunded",
		"transaction_id", providerRef,
		"refund_txn_id", resp.RefundTransactionID,
	)

	return nil
}

// handleCaptured processes txn.captured events from acquiring.
func (a *Adapter) handleCaptured(msg *nats.Msg) {
	var event struct {
		TransactionID string    `json:"transactionId"`
		Amount        int64     `json:"amount"`
		Timestamp     time.Time `json:"timestamp"`
	}
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		a.logger.Error("unmarshal captured event", "error", err)
		return
	}

	a.logger.Info("received capture event", "transaction_id", event.TransactionID)

	ctx := context.Background()
	payment, err := a.store.GetByTransactionID(ctx, event.TransactionID)
	if err != nil {
		a.logger.Error("payment not found for capture event", "transaction_id", event.TransactionID)
		return
	}

	// Update local status
	a.store.MarkCaptured(ctx, event.TransactionID)

	// Notify funding service
	if a.fundingService != nil && payment.IntentID != "" {
		if err := a.fundingService.ProcessCardPayment(ctx, payment.IntentID, event.TransactionID, true); err != nil {
			a.logger.Error("failed to process card payment in funding service", "error", err)
		}
	}
}

// handleRefunded processes txn.refunded events from acquiring.
func (a *Adapter) handleRefunded(msg *nats.Msg) {
	var event struct {
		TransactionID       string    `json:"transactionId"`
		RefundTransactionID string    `json:"refundTransactionId"`
		Amount              int64     `json:"amount"`
		Timestamp           time.Time `json:"timestamp"`
	}
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		a.logger.Error("unmarshal refunded event", "error", err)
		return
	}

	a.logger.Info("received refund event",
		"transaction_id", event.TransactionID,
		"refund_txn_id", event.RefundTransactionID,
	)

	ctx := context.Background()
	a.store.MarkRefunded(ctx, event.TransactionID)
}

// handleChargeback processes chargeback events from acquiring.
func (a *Adapter) handleChargeback(msg *nats.Msg) {
	var event ChargebackEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		a.logger.Error("unmarshal chargeback event", "error", err)
		return
	}

	a.logger.Warn("received chargeback",
		"transaction_id", event.TransactionID,
		"chargeback_id", event.ChargebackID,
		"reason", event.Reason,
		"amount", event.Amount,
	)

	ctx := context.Background()
	payment, err := a.store.GetByTransactionID(ctx, event.TransactionID)
	if err != nil {
		a.logger.Error("payment not found for chargeback", "transaction_id", event.TransactionID)
		return
	}

	// Update local status
	a.store.MarkChargeback(ctx, event.TransactionID, event.Reason)

	// Notify funding service to reverse the ledger entry
	if a.fundingService != nil && payment.IntentID != "" {
		reason := fmt.Sprintf("Chargeback: %s (%s)", event.Reason, event.ReasonCode)
		if err := a.fundingService.ProcessChargeback(ctx, payment.IntentID, reason); err != nil {
			a.logger.Error("failed to process chargeback in funding service", "error", err)
		}
	}
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
			id, tenant_id, wallet_id, customer_id, intent_id, card_token, transaction_id, auth_code,
			card_last_four, card_brand, card_type, amount_minor, currency,
			three_ds_version, three_ds_status, card_status,
			initiated_at, authorised_at, captured_at, refunded_at, chargeback_at,
			error_code, error_message, decline_reason, response_data,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)
	`

	responseData, _ := json.Marshal(payment.ResponseData)

	_, err := s.pool.Exec(ctx, query,
		payment.ID, payment.TenantID, payment.WalletID, payment.CustomerID,
		nullableString(payment.IntentID), payment.CardToken, payment.TransactionID,
		nullableString(payment.AuthCode), nullableString(payment.CardLastFour),
		payment.CardBrand, payment.CardType, payment.AmountMinor, payment.Currency,
		nullableString(payment.ThreeDSVersion), nullableString(payment.ThreeDSStatus),
		payment.Status,
		payment.InitiatedAt, payment.AuthorisedAt, payment.CapturedAt,
		payment.RefundedAt, payment.ChargebackAt,
		nullableString(payment.ErrorCode), nullableString(payment.ErrorMessage),
		nullableString(payment.DeclineReason), responseData,
		payment.CreatedAt, payment.UpdatedAt,
	)
	return err
}

// GetByTransactionID retrieves a payment by transaction ID.
func (s *Store) GetByTransactionID(ctx context.Context, txnID string) (*Payment, error) {
	query := `
		SELECT id, tenant_id, wallet_id, customer_id, intent_id, card_token, transaction_id, auth_code,
			   card_last_four, card_brand, card_type, amount_minor, currency,
			   three_ds_version, three_ds_status, card_status,
			   initiated_at, authorised_at, captured_at, refunded_at, chargeback_at,
			   error_code, error_message, decline_reason, response_data,
			   created_at, updated_at
		FROM card_payments WHERE transaction_id = $1
	`

	row := s.pool.QueryRow(ctx, query, txnID)

	var p Payment
	var intentID, authCode, lastFour, threeDSVer, threeDSStatus *string
	var errorCode, errorMsg, declineReason *string
	var responseData []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &p.WalletID, &p.CustomerID,
		&intentID, &p.CardToken, &p.TransactionID, &authCode,
		&lastFour, &p.CardBrand, &p.CardType, &p.AmountMinor, &p.Currency,
		&threeDSVer, &threeDSStatus, &p.Status,
		&p.InitiatedAt, &p.AuthorisedAt, &p.CapturedAt, &p.RefundedAt, &p.ChargebackAt,
		&errorCode, &errorMsg, &declineReason, &responseData,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("payment not found: %s", txnID)
		}
		return nil, err
	}

	if intentID != nil {
		p.IntentID = *intentID
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

	return &p, nil
}

// GetByIntentID retrieves a payment by funding intent ID.
func (s *Store) GetByIntentID(ctx context.Context, intentID string) (*Payment, error) {
	query := `
		SELECT id, tenant_id, wallet_id, customer_id, intent_id, card_token, transaction_id, auth_code,
			   card_last_four, card_brand, card_type, amount_minor, currency,
			   three_ds_version, three_ds_status, card_status,
			   initiated_at, authorised_at, captured_at, refunded_at, chargeback_at,
			   error_code, error_message, decline_reason, response_data,
			   created_at, updated_at
		FROM card_payments WHERE intent_id = $1
	`

	row := s.pool.QueryRow(ctx, query, intentID)

	var p Payment
	var iID, authCode, lastFour, threeDSVer, threeDSStatus *string
	var errorCode, errorMsg, declineReason *string
	var responseData []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &p.WalletID, &p.CustomerID,
		&iID, &p.CardToken, &p.TransactionID, &authCode,
		&lastFour, &p.CardBrand, &p.CardType, &p.AmountMinor, &p.Currency,
		&threeDSVer, &threeDSStatus, &p.Status,
		&p.InitiatedAt, &p.AuthorisedAt, &p.CapturedAt, &p.RefundedAt, &p.ChargebackAt,
		&errorCode, &errorMsg, &declineReason, &responseData,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("payment not found for intent: %s", intentID)
		}
		return nil, err
	}

	if iID != nil {
		p.IntentID = *iID
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

	return &p, nil
}

// MarkCaptured marks payment as captured.
func (s *Store) MarkCaptured(ctx context.Context, txnID string) error {
	query := `UPDATE card_payments SET card_status = $2, captured_at = $3, updated_at = $3 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, StatusCaptured, time.Now())
	return err
}

// MarkRefunded marks payment as refunded.
func (s *Store) MarkRefunded(ctx context.Context, txnID string) error {
	query := `UPDATE card_payments SET card_status = $2, refunded_at = $3, updated_at = $3 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, StatusRefunded, time.Now())
	return err
}

// MarkVoided marks payment as voided.
func (s *Store) MarkVoided(ctx context.Context, txnID string) error {
	query := `UPDATE card_payments SET card_status = $2, updated_at = $3 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, StatusVoided, time.Now())
	return err
}

// MarkChargeback marks payment as chargebacked.
func (s *Store) MarkChargeback(ctx context.Context, txnID, reason string) error {
	query := `UPDATE card_payments SET card_status = $2, chargeback_at = $3, decline_reason = $4, updated_at = $3 WHERE transaction_id = $1`
	_, err := s.pool.Exec(ctx, query, txnID, StatusChargeback, time.Now(), reason)
	return err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
