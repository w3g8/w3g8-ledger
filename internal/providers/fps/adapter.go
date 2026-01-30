// Package fps provides a Faster Payments Service (FPS) payment adapter.
package fps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"finplatform/internal/funding"
)

// Config holds FPS adapter configuration.
type Config struct {
	BaseURL     string        `env:"FPS_BASE_URL"`
	APIKey      string        `env:"FPS_API_KEY"`
	Timeout     time.Duration `env:"FPS_TIMEOUT" envDefault:"30s"`
	WebhookPath string        `env:"FPS_WEBHOOK_PATH" envDefault:"/webhooks/fps"`
}

// FPSStatus represents the status of an FPS payment.
type FPSStatus string

const (
	FPSSubmitted FPSStatus = "SUBMITTED"
	FPSAccepted  FPSStatus = "ACCEPTED"
	FPSSettled   FPSStatus = "SETTLED"
	FPSFailed    FPSStatus = "FAILED"
	FPSRecalled  FPSStatus = "RECALLED"
	FPSReturned  FPSStatus = "RETURNED"
)

// RecallReason represents the reason for a recall.
type RecallReason string

const (
	RecallDuplicate       RecallReason = "DUPL" // Duplicate payment
	RecallFraud           RecallReason = "FRAD" // Fraudulent origin
	RecallTechIssue       RecallReason = "TECH" // Technical problems
	RecallCustomerRequest RecallReason = "CUST" // Customer requested
	RecallWrongAmount     RecallReason = "AM09" // Wrong amount
	RecallWrongAccount    RecallReason = "AC03" // Wrong account
)

// FPSPayment represents an FPS payment record.
type FPSPayment struct {
	ID                string         `json:"id"`
	PaymentAttemptID  string         `json:"payment_attempt_id"`
	IntentID          string         `json:"intent_id,omitempty"`
	EndToEndID        string         `json:"end_to_end_id"`
	ProviderPaymentID string         `json:"provider_payment_id,omitempty"`
	SortCode          string         `json:"sort_code,omitempty"`
	AccountNumber     string         `json:"account_number,omitempty"`
	AmountMinor       int64          `json:"amount_minor"`
	Currency          string         `json:"currency"`
	Status            FPSStatus      `json:"fps_status"`
	SubmittedAt       time.Time      `json:"submitted_at"`
	AcceptedAt        *time.Time     `json:"accepted_at,omitempty"`
	SettledAt         *time.Time     `json:"settled_at,omitempty"`
	RecalledAt        *time.Time     `json:"recalled_at,omitempty"`
	RecallReason      RecallReason   `json:"recall_reason,omitempty"`
	RecallRef         string         `json:"recall_ref,omitempty"`
	ReturnedAt        *time.Time     `json:"returned_at,omitempty"`
	ReturnReason      string         `json:"return_reason,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	ErrorMessage      string         `json:"error_message,omitempty"`
	ResponseData      map[string]any `json:"response_data,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// SubmitRequest is the request body for FPS payment submission.
type SubmitRequest struct {
	EndToEndID    string `json:"end_to_end_id"`
	Amount        int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	CreditorName  string `json:"creditor_name"`
	SortCode      string `json:"sort_code"`
	AccountNumber string `json:"account_number"`
	Reference     string `json:"reference,omitempty"`
	IntentID      string `json:"intent_id"`
}

// SubmitResponse is the response from FPS payment submission.
type SubmitResponse struct {
	ProviderPaymentID string `json:"provider_payment_id"`
	EndToEndID        string `json:"end_to_end_id"`
	Status            string `json:"status"`
	Message           string `json:"message,omitempty"`
}

// StatusResponse is the response from FPS status query.
type StatusResponse struct {
	EndToEndID        string     `json:"end_to_end_id"`
	ProviderPaymentID string     `json:"provider_payment_id"`
	Status            string     `json:"status"` // SUBMITTED, ACCEPTED, SETTLED, FAILED, RECALLED, RETURNED
	SettledAt         *time.Time `json:"settled_at,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
}

// RecallRequest is the request to recall an FPS payment.
type RecallRequest struct {
	EndToEndID string       `json:"end_to_end_id"`
	Reason     RecallReason `json:"reason"`
	Comment    string       `json:"comment,omitempty"`
}

// RecallResponse is the response from a recall request.
type RecallResponse struct {
	RecallRef string `json:"recall_ref"`
	Status    string `json:"status"` // ACCEPTED, REJECTED, PENDING
	Message   string `json:"message,omitempty"`
}

// ReturnNotification represents an inbound return from the receiving bank.
type ReturnNotification struct {
	OriginalEndToEndID string    `json:"original_end_to_end_id"`
	ReturnReason       string    `json:"return_reason"` // AC03, AM04, etc.
	ReturnReasonDesc   string    `json:"return_reason_desc"`
	ReturnedAt         time.Time `json:"returned_at"`
	AmountMinor        int64     `json:"amount_minor"`
}

// Adapter implements the FPS payment provider.
type Adapter struct {
	config         Config
	httpClient     *http.Client
	store          Store
	fundingService FundingService
	logger         *slog.Logger
}

// Store defines the FPS payment persistence interface.
type Store interface {
	Create(ctx context.Context, payment *FPSPayment) error
	GetByEndToEndID(ctx context.Context, endToEndID string) (*FPSPayment, error)
	UpdateStatus(ctx context.Context, endToEndID string, status FPSStatus, providerPaymentID string, responseData map[string]any) error
	MarkSettled(ctx context.Context, endToEndID string, settledAt time.Time) error
	MarkFailed(ctx context.Context, endToEndID string, errorCode, errorMessage string) error
	MarkRecalled(ctx context.Context, endToEndID string, recallRef string, reason RecallReason, recalledAt time.Time) error
	MarkReturned(ctx context.Context, endToEndID string, returnReason string, returnedAt time.Time) error
	GetSettledPayments(ctx context.Context, olderThan time.Duration, limit int) ([]*FPSPayment, error)
}

// FundingService callback interface.
type FundingService interface {
	ProcessInboundCredit(ctx context.Context, event *funding.InboundCreditEvent) error
	ProcessChargeback(ctx context.Context, intentID, reason string) error
}

// NewAdapter creates a new FPS adapter.
func NewAdapter(cfg Config, store Store, logger *slog.Logger) *Adapter {
	return &Adapter{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		store:  store,
		logger: logger,
	}
}

// SetFundingService sets the funding service callback.
func (a *Adapter) SetFundingService(svc FundingService) {
	a.fundingService = svc
}

// Submit implements FPSProvider.Submit - submits a payment to FPS for funding.
// Returns the end_to_end_id as the provider reference.
func (a *Adapter) Submit(ctx context.Context, intent *funding.FundingIntent, attemptID string) (providerRef string, err error) {
	// Generate unique end-to-end ID
	endToEndID := fmt.Sprintf("E2E%s", ulid.Make().String())

	// Get bank details from intent
	var sortCode, accountNumber string
	if intent.BankDetails != nil {
		sortCode = intent.BankDetails.SortCode
		accountNumber = intent.BankDetails.AccountNumber
	}

	req := SubmitRequest{
		EndToEndID:    endToEndID,
		Amount:        intent.Amount.AmountMinor,
		Currency:      string(intent.Amount.Currency),
		CreditorName:  intent.CustomerID, // Would come from customer lookup
		SortCode:      sortCode,
		AccountNumber: accountNumber,
		Reference:     intent.BankDetails.Reference,
		IntentID:      intent.ID,
	}

	a.logger.Info("submitting FPS payment",
		"intent_id", intent.ID,
		"end_to_end_id", endToEndID,
		"amount", intent.Amount.AmountMinor,
	)

	// Create FPS payment record
	fpsPayment := &FPSPayment{
		ID:               ulid.Make().String(),
		PaymentAttemptID: attemptID,
		IntentID:         intent.ID,
		EndToEndID:       endToEndID,
		SortCode:         sortCode,
		AccountNumber:    accountNumber,
		AmountMinor:      intent.Amount.AmountMinor,
		Currency:         string(intent.Amount.Currency),
		Status:           FPSSubmitted,
		SubmittedAt:      time.Now(),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := a.store.Create(ctx, fpsPayment); err != nil {
		return "", fmt.Errorf("create fps payment record: %w", err)
	}

	// Submit to FPS API
	resp, err := a.doSubmit(ctx, req)
	if err != nil {
		// Update record with error
		a.store.MarkFailed(ctx, endToEndID, "SUBMIT_ERROR", err.Error())
		return "", fmt.Errorf("fps submit: %w", err)
	}

	// Update record with provider response
	a.store.UpdateStatus(ctx, endToEndID, FPSStatus(resp.Status), resp.ProviderPaymentID, map[string]any{
		"response": resp,
	})

	a.logger.Info("FPS payment submitted",
		"intent_id", intent.ID,
		"end_to_end_id", endToEndID,
		"provider_payment_id", resp.ProviderPaymentID,
	)

	return endToEndID, nil
}

func (a *Adapter) doSubmit(ctx context.Context, req SubmitRequest) (*SubmitResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.BaseURL+"/payments", bytes.NewReader(body))
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

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("fps api error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp SubmitResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// GetStatus implements FPSProvider.GetStatus - retrieves the status of an FPS payment.
func (a *Adapter) GetStatus(ctx context.Context, providerRef string) (status string, settledAt *time.Time, err error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.config.BaseURL+"/payments/"+providerRef, nil)
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("fps api error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp StatusResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return resp.Status, resp.SettledAt, nil
}

// Recall initiates a recall for a settled FPS payment.
// FPS recalls must be initiated within minutes of settlement.
func (a *Adapter) Recall(ctx context.Context, endToEndID string, reason RecallReason, comment string) (*RecallResponse, error) {
	// Get the payment to verify it can be recalled
	payment, err := a.store.GetByEndToEndID(ctx, endToEndID)
	if err != nil {
		return nil, fmt.Errorf("get payment: %w", err)
	}

	if payment.Status != FPSSettled {
		return nil, fmt.Errorf("can only recall settled payments, current status: %s", payment.Status)
	}

	// Check recall window (FPS typically allows recalls within ~15 minutes)
	if payment.SettledAt != nil && time.Since(*payment.SettledAt) > 15*time.Minute {
		return nil, fmt.Errorf("recall window expired (settled at %s)", payment.SettledAt)
	}

	a.logger.Info("initiating FPS recall",
		"end_to_end_id", endToEndID,
		"reason", reason,
	)

	req := RecallRequest{
		EndToEndID: endToEndID,
		Reason:     reason,
		Comment:    comment,
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.BaseURL+"/payments/"+endToEndID+"/recall", bytes.NewReader(body))
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

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("fps recall error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp RecallResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Update local record
	if resp.Status == "ACCEPTED" {
		now := time.Now()
		if err := a.store.MarkRecalled(ctx, endToEndID, resp.RecallRef, reason, now); err != nil {
			a.logger.Error("failed to update recall status", "error", err)
		}
	}

	a.logger.Info("FPS recall initiated",
		"end_to_end_id", endToEndID,
		"recall_ref", resp.RecallRef,
		"status", resp.Status,
	)

	return &resp, nil
}

// HandleReturn processes an inbound return notification from the receiving bank.
func (a *Adapter) HandleReturn(ctx context.Context, notification *ReturnNotification) error {
	a.logger.Info("processing FPS return",
		"original_e2e_id", notification.OriginalEndToEndID,
		"return_reason", notification.ReturnReason,
	)

	// Get the original payment
	payment, err := a.store.GetByEndToEndID(ctx, notification.OriginalEndToEndID)
	if err != nil {
		return fmt.Errorf("get original payment: %w", err)
	}

	if payment.Status != FPSSettled && payment.Status != FPSRecalled {
		a.logger.Warn("unexpected return for payment",
			"end_to_end_id", notification.OriginalEndToEndID,
			"current_status", payment.Status,
		)
	}

	// Mark as returned
	if err := a.store.MarkReturned(ctx, notification.OriginalEndToEndID, notification.ReturnReason, notification.ReturnedAt); err != nil {
		return fmt.Errorf("mark returned: %w", err)
	}

	// Notify funding service to reverse the ledger entry
	if a.fundingService != nil && payment.IntentID != "" {
		reason := fmt.Sprintf("FPS Return: %s - %s", notification.ReturnReason, notification.ReturnReasonDesc)
		if err := a.fundingService.ProcessChargeback(ctx, payment.IntentID, reason); err != nil {
			a.logger.Error("failed to process return in funding service", "error", err)
		}
	}

	a.logger.Info("FPS payment returned",
		"end_to_end_id", notification.OriginalEndToEndID,
		"return_reason", notification.ReturnReason,
		"amount", notification.AmountMinor,
	)

	return nil
}

// ProviderName returns the provider name for this adapter.
func (a *Adapter) ProviderName() string {
	return "fps"
}
