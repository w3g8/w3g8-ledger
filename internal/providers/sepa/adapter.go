// Package sepa provides a SEPA SCT (Single Credit Transfer) payment adapter.
package sepa

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

// Config holds SEPA adapter configuration.
type Config struct {
	BaseURL            string        `env:"SEPA_BASE_URL"`
	APIKey             string        `env:"SEPA_API_KEY"`
	Timeout            time.Duration `env:"SEPA_TIMEOUT" envDefault:"30s"`
	ReportPollInterval time.Duration `env:"SEPA_REPORT_POLL" envDefault:"5m"`
}

// SEPAStatus represents the status of a SEPA payment.
type SEPAStatus string

const (
	SEPASubmitted SEPAStatus = "SUBMITTED"
	SEPAAccepted  SEPAStatus = "ACCEPTED"
	SEPARejected  SEPAStatus = "REJECTED"
	SEPASettled   SEPAStatus = "SETTLED"
)

// SEPAPayment represents a SEPA payment record.
type SEPAPayment struct {
	ID               string     `json:"id"`
	PaymentAttemptID string     `json:"payment_attempt_id"`
	MsgID            string     `json:"msg_id"`
	PmtInfID         string     `json:"pmt_inf_id"`
	EndToEndID       string     `json:"end_to_end_id"`
	IBAN             string     `json:"iban"`
	BIC              string     `json:"bic,omitempty"`
	CreditorName     string     `json:"creditor_name,omitempty"`
	Status           SEPAStatus `json:"sepa_status"`
	SubmittedAt      time.Time  `json:"submitted_at"`
	AcceptedAt       *time.Time `json:"accepted_at,omitempty"`
	SettledAt        *time.Time `json:"settled_at,omitempty"`
	RejectReasonCode string     `json:"reject_reason_code,omitempty"`
	RejectReasonDesc string     `json:"reject_reason_desc,omitempty"`
	LastReportID     string     `json:"last_report_id,omitempty"`
	LastReportAt     *time.Time `json:"last_report_at,omitempty"`
	ResponseData     map[string]any `json:"response_data,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// SubmitRequest is the request body for SEPA payment submission.
type SubmitRequest struct {
	MsgID           string `json:"msg_id"`
	PmtInfID        string `json:"pmt_inf_id"`
	EndToEndID      string `json:"end_to_end_id"`
	Amount          int64  `json:"amount_minor"`
	Currency        string `json:"currency"`
	CreditorName    string `json:"creditor_name"`
	CreditorIBAN    string `json:"creditor_iban"`
	CreditorBIC     string `json:"creditor_bic,omitempty"`
	Reference       string `json:"reference,omitempty"`
	PaymentIntentID string `json:"payment_intent_id"`
}

// SubmitResponse is the response from SEPA payment submission.
type SubmitResponse struct {
	MsgID    string `json:"msg_id"`
	PmtInfID string `json:"pmt_inf_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

// StatusResponse is the response from SEPA status query.
type StatusResponse struct {
	MsgID            string     `json:"msg_id"`
	PmtInfID         string     `json:"pmt_inf_id"`
	EndToEndID       string     `json:"end_to_end_id"`
	Status           string     `json:"status"` // SUBMITTED, ACCEPTED, REJECTED, SETTLED
	SettledAt        *time.Time `json:"settled_at,omitempty"`
	RejectReasonCode string     `json:"reject_reason_code,omitempty"`
	RejectReasonDesc string     `json:"reject_reason_desc,omitempty"`
}

// PaymentRoute holds routing information for a payment.
type PaymentRoute struct {
	Rail          domain.Rail
	Provider      string
	PolicyVersion int
}

// Adapter implements the SEPA SCT payment provider.
type Adapter struct {
	config     Config
	httpClient *http.Client
	store      Store
	logger     *slog.Logger
}

// Store defines the SEPA payment persistence interface.
type Store interface {
	Create(ctx context.Context, payment *SEPAPayment) error
	GetByMsgAndPmtInf(ctx context.Context, msgID, pmtInfID string) (*SEPAPayment, error)
	GetByEndToEndID(ctx context.Context, endToEndID string) (*SEPAPayment, error)
	UpdateStatus(ctx context.Context, msgID, pmtInfID string, status SEPAStatus, responseData map[string]any) error
	MarkAccepted(ctx context.Context, msgID, pmtInfID string, acceptedAt time.Time) error
	MarkSettled(ctx context.Context, msgID, pmtInfID string, settledAt time.Time) error
	MarkRejected(ctx context.Context, msgID, pmtInfID string, reasonCode, reasonDesc string) error
	GetPendingPayments(ctx context.Context, olderThan time.Duration, limit int) ([]*SEPAPayment, error)
}

// NewAdapter creates a new SEPA adapter.
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

// Submit submits a payment to SEPA.
// Returns a composite provider reference (msg_id:pmt_inf_id).
func (a *Adapter) Submit(ctx context.Context, pi *domain.PaymentIntent, route *PaymentRoute, attemptID string) (providerRef string, err error) {
	// Generate SEPA identifiers
	msgID := fmt.Sprintf("MSG%s", ulid.Make().String())
	pmtInfID := fmt.Sprintf("PMT%s", ulid.Make().String())
	endToEndID := fmt.Sprintf("E2E%s", ulid.Make().String())

	req := SubmitRequest{
		MsgID:           msgID,
		PmtInfID:        pmtInfID,
		EndToEndID:      endToEndID,
		Amount:          pi.Amount.AmountMinor,
		Currency:        string(pi.Amount.Currency),
		CreditorName:    pi.Destination.Name,
		CreditorIBAN:    pi.Destination.IBAN,
		CreditorBIC:     "", // Optional for SEPA
		Reference:       pi.CorrelationID,
		PaymentIntentID: string(pi.ID),
	}

	a.logger.Info("submitting SEPA payment",
		"payment_intent_id", pi.ID,
		"msg_id", msgID,
		"pmt_inf_id", pmtInfID,
		"amount", pi.Amount.AmountMinor,
	)

	// Create SEPA payment record
	sepaPayment := &SEPAPayment{
		ID:               ulid.Make().String(),
		PaymentAttemptID: attemptID,
		MsgID:            msgID,
		PmtInfID:         pmtInfID,
		EndToEndID:       endToEndID,
		IBAN:             pi.Destination.IBAN,
		CreditorName:     pi.Destination.Name,
		Status:           SEPASubmitted,
		SubmittedAt:      time.Now(),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := a.store.Create(ctx, sepaPayment); err != nil {
		return "", fmt.Errorf("create sepa payment record: %w", err)
	}

	// Submit to SEPA API
	resp, err := a.doSubmit(ctx, req)
	if err != nil {
		// Update record with error
		a.store.MarkRejected(ctx, msgID, pmtInfID, "SUBMIT_ERROR", err.Error())
		return "", fmt.Errorf("sepa submit: %w", err)
	}

	// Update record with provider response
	a.store.UpdateStatus(ctx, msgID, pmtInfID, SEPAStatus(resp.Status), map[string]any{
		"response": resp,
	})

	a.logger.Info("SEPA payment submitted",
		"payment_intent_id", pi.ID,
		"msg_id", msgID,
		"pmt_inf_id", pmtInfID,
	)

	// Return composite reference
	return fmt.Sprintf("%s:%s", msgID, pmtInfID), nil
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
		return nil, fmt.Errorf("sepa api error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp SubmitResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// GetStatus retrieves the status of a SEPA payment.
func (a *Adapter) GetStatus(ctx context.Context, msgID, pmtInfID string) (*StatusResponse, error) {
	url := fmt.Sprintf("%s/payments/%s/%s", a.config.BaseURL, msgID, pmtInfID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, fmt.Errorf("sepa api error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp StatusResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// ProviderName returns the provider name for this adapter.
func (a *Adapter) ProviderName() string {
	return "sepa"
}
