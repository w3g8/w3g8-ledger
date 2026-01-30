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

	"finplatform/internal/domain"
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
)

// FPSPayment represents an FPS payment record.
type FPSPayment struct {
	ID                string     `json:"id"`
	PaymentAttemptID  string     `json:"payment_attempt_id"`
	EndToEndID        string     `json:"end_to_end_id"`
	ProviderPaymentID string     `json:"provider_payment_id,omitempty"`
	SortCode          string     `json:"sort_code,omitempty"`
	AccountNumber     string     `json:"account_number,omitempty"`
	Status            FPSStatus  `json:"fps_status"`
	SubmittedAt       time.Time  `json:"submitted_at"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
	SettledAt         *time.Time `json:"settled_at,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	ResponseData      map[string]any `json:"response_data,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// SubmitRequest is the request body for FPS payment submission.
type SubmitRequest struct {
	EndToEndID     string `json:"end_to_end_id"`
	Amount         int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	CreditorName   string `json:"creditor_name"`
	SortCode       string `json:"sort_code"`
	AccountNumber  string `json:"account_number"`
	Reference      string `json:"reference,omitempty"`
	PaymentIntentID string `json:"payment_intent_id"`
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
	Status            string     `json:"status"` // SUBMITTED, ACCEPTED, SETTLED, FAILED
	SettledAt         *time.Time `json:"settled_at,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
}

// PaymentRoute holds routing information for a payment.
type PaymentRoute struct {
	Rail          domain.Rail
	Provider      string
	PolicyVersion int
}

// Adapter implements the FPS payment provider.
type Adapter struct {
	config     Config
	httpClient *http.Client
	store      Store
	logger     *slog.Logger
}

// Store defines the FPS payment persistence interface.
type Store interface {
	Create(ctx context.Context, payment *FPSPayment) error
	GetByEndToEndID(ctx context.Context, endToEndID string) (*FPSPayment, error)
	UpdateStatus(ctx context.Context, endToEndID string, status FPSStatus, providerPaymentID string, responseData map[string]any) error
	MarkSettled(ctx context.Context, endToEndID string, settledAt time.Time) error
	MarkFailed(ctx context.Context, endToEndID string, errorCode, errorMessage string) error
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

// Submit submits a payment to FPS.
// Returns the end_to_end_id as the provider reference.
func (a *Adapter) Submit(ctx context.Context, pi *domain.PaymentIntent, route *PaymentRoute, attemptID string) (providerRef string, err error) {
	// Generate unique end-to-end ID
	endToEndID := fmt.Sprintf("E2E%s", ulid.Make().String())

	// Extract sort code and account number from destination
	sortCode, accountNumber := a.extractUKBankDetails(pi.Destination)

	req := SubmitRequest{
		EndToEndID:      endToEndID,
		Amount:          pi.Amount.AmountMinor,
		Currency:        string(pi.Amount.Currency),
		CreditorName:    pi.Destination.Name,
		SortCode:        sortCode,
		AccountNumber:   accountNumber,
		Reference:       pi.CorrelationID,
		PaymentIntentID: string(pi.ID),
	}

	a.logger.Info("submitting FPS payment",
		"payment_intent_id", pi.ID,
		"end_to_end_id", endToEndID,
		"amount", pi.Amount.AmountMinor,
	)

	// Create FPS payment record
	fpsPayment := &FPSPayment{
		ID:               ulid.Make().String(),
		PaymentAttemptID: attemptID,
		EndToEndID:       endToEndID,
		SortCode:         sortCode,
		AccountNumber:    accountNumber,
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
		"payment_intent_id", pi.ID,
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

// GetStatus retrieves the status of an FPS payment.
func (a *Adapter) GetStatus(ctx context.Context, endToEndID string) (*StatusResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.config.BaseURL+"/payments/"+endToEndID, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

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

	var resp StatusResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// extractUKBankDetails extracts sort code and account number from destination.
func (a *Adapter) extractUKBankDetails(dest domain.Destination) (sortCode, accountNumber string) {
	// If destination has explicit sort code fields, use those
	if dest.Account != "" {
		// Assume format: "XXXXXX-XXXXXXXX" (sort code - account number)
		// or destination.Account is the account number
		accountNumber = dest.Account
	}

	// For UK FPS, we might need to derive from IBAN or other fields
	// This is simplified - real implementation would have proper parsing
	if dest.IBAN != "" && len(dest.IBAN) > 14 {
		// UK IBAN: GB82 WEST 1234 5698 7654 32
		// Sort code is chars 8-14, account is 14-22
		sortCode = dest.IBAN[8:14]
		accountNumber = dest.IBAN[14:22]
	}

	return sortCode, accountNumber
}

// ProviderName returns the provider name for this adapter.
func (a *Adapter) ProviderName() string {
	return "fps"
}
