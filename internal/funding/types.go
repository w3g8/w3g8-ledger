// Package domain contains core domain types for the vertical slice.
package domain

import "time"

// TenantID identifies a tenant.
type TenantID string

// CustomerID identifies a customer.
type CustomerID string

// DepositID identifies a deposit.
type DepositID string

// PaymentIntentID identifies a payment intent.
type PaymentIntentID string

// HoldID identifies a wallet hold.
type HoldID string

// FeeQuoteID identifies a fee quote.
type FeeQuoteID string

// AffiliateID identifies an affiliate.
type AffiliateID string

// Currency is a 3-letter ISO 4217 code.
type Currency string

const (
	USD Currency = "USD"
	GBP Currency = "GBP"
	EUR Currency = "EUR"
)

// Money represents a monetary amount in minor units (cents/pence).
type Money struct {
	AmountMinor int64    `json:"amount_minor"`
	Currency    Currency `json:"currency"`
}

// KYCStatus represents KYC verification status.
type KYCStatus string

const (
	KYCPending  KYCStatus = "PENDING"
	KYCApproved KYCStatus = "APPROVED"
	KYCRejected KYCStatus = "REJECTED"
	KYCReview   KYCStatus = "REVIEW"
)

// KYCLevel represents the KYC verification level.
type KYCLevel string

const (
	KYCL0 KYCLevel = "L0"
	KYCL1 KYCLevel = "L1"
	KYCL2 KYCLevel = "L2"
)

// Customer represents a customer in the system.
type Customer struct {
	ID          CustomerID  `json:"id"`
	TenantID    TenantID    `json:"tenant_id"`
	ExternalID  string      `json:"external_id,omitempty"`
	Country     string      `json:"country"`
	Segment     string      `json:"segment"`
	AffiliateID AffiliateID `json:"affiliate_id,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
}

// KYCInfo contains KYC verification information.
type KYCInfo struct {
	Status       KYCStatus `json:"status"`
	Level        KYCLevel  `json:"level"`
	RiskScore    int       `json:"risk_score"`
	PEPHit       bool      `json:"pep_hit"`
	SanctionsHit bool      `json:"sanctions_hit"`
}

// DepositStatus represents the status of a deposit.
type DepositStatus string

const (
	DepositInbound    DepositStatus = "INBOUND"
	DepositReconciled DepositStatus = "RECONCILED"
	DepositCredited   DepositStatus = "CREDITED"
	DepositHeld       DepositStatus = "HELD"
	DepositRejected   DepositStatus = "REJECTED"
)

// Rail identifies a payment rail.
type Rail string

const (
	RailFPS  Rail = "FPS"
	RailSEPA Rail = "SEPA"
	RailACH  Rail = "ACH"
	RailWire Rail = "WIRE"
)

// Deposit represents an inbound deposit.
type Deposit struct {
	ID          DepositID     `json:"id"`
	TenantID    TenantID      `json:"tenant_id"`
	ExternalRef string        `json:"external_ref"`
	Rail        Rail          `json:"rail"`
	Amount      Money         `json:"amount"`
	Status      DepositStatus `json:"status"`
	CustomerID  CustomerID    `json:"customer_id,omitempty"`
	ReceivedAt  time.Time     `json:"received_at"`
	CreditedAt  *time.Time    `json:"credited_at,omitempty"`
}

// PaymentIntentStatus represents the status of a payment intent.
type PaymentIntentStatus string

const (
	PICreated    PaymentIntentStatus = "CREATED"
	PIPrechecked PaymentIntentStatus = "PRECHECKED"
	PIReserved   PaymentIntentStatus = "RESERVED"
	PIRouted     PaymentIntentStatus = "ROUTED"
	PISubmitted  PaymentIntentStatus = "SUBMITTED"
	PISettled    PaymentIntentStatus = "SETTLED"
	PIFailed     PaymentIntentStatus = "FAILED"
)

// Destination represents a payment destination.
type Destination struct {
	Type    string `json:"type"` // IBAN, SORT_CODE, etc.
	IBAN    string `json:"iban,omitempty"`
	Account string `json:"account,omitempty"`
	Name    string `json:"name"`
	Country string `json:"country,omitempty"`
}

// PaymentIntent represents an outbound payment request.
type PaymentIntent struct {
	ID             PaymentIntentID     `json:"id"`
	TenantID       TenantID            `json:"tenant_id"`
	CustomerID     CustomerID          `json:"customer_id"`
	Amount         Money               `json:"amount"`
	Destination    Destination         `json:"destination"`
	RailHint       Rail                `json:"rail_hint,omitempty"`
	Status         PaymentIntentStatus `json:"status"`
	IdempotencyKey string              `json:"idempotency_key"`
	CorrelationID  string              `json:"correlation_id,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
}

// HoldStatus represents the status of a wallet hold.
type HoldStatus string

const (
	HoldActive   HoldStatus = "ACTIVE"
	HoldReleased HoldStatus = "RELEASED"
	HoldConsumed HoldStatus = "CONSUMED"
	HoldExpired  HoldStatus = "EXPIRED"
)

// WalletHold represents a hold on wallet funds.
type WalletHold struct {
	ID         HoldID     `json:"id"`
	TenantID   TenantID   `json:"tenant_id"`
	CustomerID CustomerID `json:"customer_id"`
	Amount     Money      `json:"amount"`
	Status     HoldStatus `json:"status"`
	Reference  string     `json:"reference,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// PaymentAttemptID identifies a payment attempt.
type PaymentAttemptID string

// PaymentAttemptStatus represents the status of a payment attempt.
type PaymentAttemptStatus string

const (
	AttemptPending   PaymentAttemptStatus = "PENDING"
	AttemptSubmitted PaymentAttemptStatus = "SUBMITTED"
	AttemptSettled   PaymentAttemptStatus = "SETTLED"
	AttemptFailed    PaymentAttemptStatus = "FAILED"
)

// PaymentAttempt tracks a submission attempt to a payment provider.
type PaymentAttempt struct {
	ID               PaymentAttemptID     `json:"id"`
	PaymentIntentID  PaymentIntentID      `json:"payment_intent_id"`
	Provider         string               `json:"provider"`
	ProviderKey      string               `json:"provider_key"`
	ProviderRef      *string              `json:"provider_ref,omitempty"`
	Status           PaymentAttemptStatus `json:"status"`
	ErrorCode        *string              `json:"error_code,omitempty"`
	ErrorMessage     *string              `json:"error_message,omitempty"`
	AttemptNumber    int                  `json:"attempt_number"`
	ProviderMetadata map[string]any       `json:"provider_metadata,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
	SubmittedAt      *time.Time           `json:"submitted_at,omitempty"`
	SettledAt        *time.Time           `json:"settled_at,omitempty"`
}
