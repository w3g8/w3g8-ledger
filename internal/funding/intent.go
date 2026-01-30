// Package funding provides wallet funding operations across all payment rails.
package funding

import (
	"errors"
	"time"

	"finplatform/internal/common/money"
)

// Method represents a funding method.
type Method string

const (
	MethodOpenBanking Method = "OPEN_BANKING"
	MethodSEPA        Method = "SEPA"
	MethodFPS         Method = "FPS"
	MethodCard        Method = "CARD"
	MethodACH         Method = "ACH"
)

// IntentStatus represents the status of a funding intent.
type IntentStatus string

const (
	IntentCreated   IntentStatus = "created"
	IntentPending   IntentStatus = "pending"
	IntentSettled   IntentStatus = "settled"
	IntentFailed    IntentStatus = "failed"
	IntentExpired   IntentStatus = "expired"
	IntentReversed  IntentStatus = "reversed"
)

// FundingIntent represents a request to fund a wallet.
// This is the unified entrypoint for all funding methods.
type FundingIntent struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	WalletID       string            `json:"wallet_id"`
	CustomerID     string            `json:"customer_id"`
	Amount         money.Money       `json:"amount"`
	Method         Method            `json:"method"`
	Status         IntentStatus      `json:"status"`
	IdempotencyKey string            `json:"idempotency_key"`

	// Provider-specific fields
	ProviderRef    string            `json:"provider_ref,omitempty"`
	RedirectURL    string            `json:"redirect_url,omitempty"`    // For Open Banking
	BankDetails    *BankDetails      `json:"bank_details,omitempty"`    // For SEPA/FPS inbound
	PaymentSession string            `json:"payment_session,omitempty"` // For cards

	// Tracking
	AttemptCount   int               `json:"attempt_count"`
	LastAttemptAt  *time.Time        `json:"last_attempt_at,omitempty"`
	SettledAt      *time.Time        `json:"settled_at,omitempty"`
	ReversedAt     *time.Time        `json:"reversed_at,omitempty"`
	ReversalReason string            `json:"reversal_reason,omitempty"`

	// Ledger reference
	LedgerBatchID  string            `json:"ledger_batch_id,omitempty"`

	// Metadata
	Metadata       map[string]string `json:"metadata,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`

	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
}

// BankDetails holds bank account details for inbound transfers.
type BankDetails struct {
	IBAN          string `json:"iban,omitempty"`
	SortCode      string `json:"sort_code,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	BIC           string `json:"bic,omitempty"`
	Reference     string `json:"reference"` // Unique reference for matching
}

// FundingAttempt tracks a single attempt to process a funding intent.
type FundingAttempt struct {
	ID              string            `json:"id"`
	IntentID        string            `json:"intent_id"`
	Provider        string            `json:"provider"`
	ProviderRef     string            `json:"provider_ref,omitempty"`
	Status          AttemptStatus     `json:"status"`
	AttemptNumber   int               `json:"attempt_number"`
	ErrorCode       string            `json:"error_code,omitempty"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	ProviderData    map[string]any    `json:"provider_data,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	SubmittedAt     *time.Time        `json:"submitted_at,omitempty"`
	SettledAt       *time.Time        `json:"settled_at,omitempty"`
}

// AttemptStatus represents the status of a funding attempt.
type AttemptStatus string

const (
	AttemptPending   AttemptStatus = "pending"
	AttemptSubmitted AttemptStatus = "submitted"
	AttemptSettled   AttemptStatus = "settled"
	AttemptFailed    AttemptStatus = "failed"
)

// NewFundingIntent creates a new funding intent.
func NewFundingIntent(id, tenantID, walletID, customerID string, amount money.Money, method Method, idempotencyKey string) (*FundingIntent, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	if tenantID == "" {
		return nil, errors.New("tenant_id is required")
	}
	if walletID == "" {
		return nil, errors.New("wallet_id is required")
	}
	if amount.AmountMinor <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if idempotencyKey == "" {
		return nil, errors.New("idempotency_key is required")
	}

	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour) // Default 24h expiry

	return &FundingIntent{
		ID:             id,
		TenantID:       tenantID,
		WalletID:       walletID,
		CustomerID:     customerID,
		Amount:         amount,
		Method:         method,
		Status:         IntentCreated,
		IdempotencyKey: idempotencyKey,
		Metadata:       make(map[string]string),
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      &expiry,
	}, nil
}

// MarkPending transitions intent to pending state.
func (i *FundingIntent) MarkPending(providerRef string) error {
	if i.Status != IntentCreated {
		return errors.New("can only mark created intents as pending")
	}
	i.Status = IntentPending
	i.ProviderRef = providerRef
	i.UpdatedAt = time.Now().UTC()
	return nil
}

// MarkSettled transitions intent to settled state.
func (i *FundingIntent) MarkSettled(ledgerBatchID string) error {
	if i.Status != IntentPending {
		return errors.New("can only settle pending intents")
	}
	now := time.Now().UTC()
	i.Status = IntentSettled
	i.LedgerBatchID = ledgerBatchID
	i.SettledAt = &now
	i.UpdatedAt = now
	return nil
}

// MarkFailed transitions intent to failed state.
func (i *FundingIntent) MarkFailed(errorCode, errorMessage string) error {
	if i.Status == IntentSettled || i.Status == IntentReversed {
		return errors.New("cannot fail settled or reversed intent")
	}
	i.Status = IntentFailed
	i.ErrorCode = errorCode
	i.ErrorMessage = errorMessage
	i.UpdatedAt = time.Now().UTC()
	return nil
}

// MarkReversed transitions intent to reversed state (for chargebacks, recalls).
func (i *FundingIntent) MarkReversed(reason string) error {
	if i.Status != IntentSettled {
		return errors.New("can only reverse settled intents")
	}
	now := time.Now().UTC()
	i.Status = IntentReversed
	i.ReversedAt = &now
	i.ReversalReason = reason
	i.UpdatedAt = now
	return nil
}

// IsTerminal returns true if the intent is in a terminal state.
func (i *FundingIntent) IsTerminal() bool {
	return i.Status == IntentSettled || i.Status == IntentFailed ||
		   i.Status == IntentExpired || i.Status == IntentReversed
}
