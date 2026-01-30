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
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"finplatform/internal/funding"
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
	SEPARecalled  SEPAStatus = "RECALLED"
	SEPAReturned  SEPAStatus = "RETURNED"
)

// SEPARecallReason represents the reason for a recall.
type SEPARecallReason string

const (
	SEPARecallDuplicate       SEPARecallReason = "DUPL" // Duplicate payment
	SEPARecallFraud           SEPARecallReason = "FRAD" // Fraudulent origin
	SEPARecallTechIssue       SEPARecallReason = "TECH" // Technical problems
	SEPARecallCustomerRequest SEPARecallReason = "CUST" // Customer requested
	SEPARecallWrongAmount     SEPARecallReason = "AM09" // Wrong amount
	SEPARecallWrongAccount    SEPARecallReason = "AC03" // Wrong account
)

// SEPAPayment represents a SEPA payment record.
type SEPAPayment struct {
	ID                   string           `json:"id"`
	PaymentAttemptID     string           `json:"payment_attempt_id"`
	IntentID             string           `json:"intent_id,omitempty"`
	MsgID                string           `json:"msg_id"`
	PmtInfID             string           `json:"pmt_inf_id"`
	EndToEndID           string           `json:"end_to_end_id"`
	IBAN                 string           `json:"iban"`
	BIC                  string           `json:"bic,omitempty"`
	CreditorName         string           `json:"creditor_name,omitempty"`
	AmountMinor          int64            `json:"amount_minor"`
	Currency             string           `json:"currency"`
	Status               SEPAStatus       `json:"sepa_status"`
	SubmittedAt          time.Time        `json:"submitted_at"`
	AcceptedAt           *time.Time       `json:"accepted_at,omitempty"`
	SettledAt            *time.Time       `json:"settled_at,omitempty"`
	RecalledAt           *time.Time       `json:"recalled_at,omitempty"`
	RecallReason         SEPARecallReason `json:"recall_reason,omitempty"`
	RecallRef            string           `json:"recall_ref,omitempty"`
	RecallAdditionalInfo string           `json:"recall_additional_info,omitempty"`
	ReturnedAt           *time.Time       `json:"returned_at,omitempty"`
	ReturnReason         string           `json:"return_reason,omitempty"`
	RejectReasonCode     string           `json:"reject_reason_code,omitempty"`
	RejectReasonDesc     string           `json:"reject_reason_desc,omitempty"`
	LastReportID         string           `json:"last_report_id,omitempty"`
	LastReportAt         *time.Time       `json:"last_report_at,omitempty"`
	ResponseData         map[string]any   `json:"response_data,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

// SubmitRequest is the request body for SEPA payment submission.
type SubmitRequest struct {
	MsgID        string `json:"msg_id"`
	PmtInfID     string `json:"pmt_inf_id"`
	EndToEndID   string `json:"end_to_end_id"`
	Amount       int64  `json:"amount_minor"`
	Currency     string `json:"currency"`
	CreditorName string `json:"creditor_name"`
	CreditorIBAN string `json:"creditor_iban"`
	CreditorBIC  string `json:"creditor_bic,omitempty"`
	Reference    string `json:"reference,omitempty"`
	IntentID     string `json:"intent_id"`
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
	Status           string     `json:"status"` // SUBMITTED, ACCEPTED, REJECTED, SETTLED, RECALLED, RETURNED
	SettledAt        *time.Time `json:"settled_at,omitempty"`
	RejectReasonCode string     `json:"reject_reason_code,omitempty"`
	RejectReasonDesc string     `json:"reject_reason_desc,omitempty"`
}

// RecallRequest is the request to recall a SEPA payment.
type RecallRequest struct {
	MsgID          string           `json:"msg_id"`
	PmtInfID       string           `json:"pmt_inf_id"`
	Reason         SEPARecallReason `json:"reason"`
	AdditionalInfo string           `json:"additional_info,omitempty"`
}

// RecallResponse is the response from a recall request.
type RecallResponse struct {
	RecallRef string `json:"recall_ref"`
	Status    string `json:"status"` // ACCEPTED, REJECTED, PENDING
	Message   string `json:"message,omitempty"`
}

// ReturnNotification represents an inbound return.
type ReturnNotification struct {
	OriginalMsgID    string    `json:"original_msg_id"`
	OriginalPmtInfID string    `json:"original_pmt_inf_id"`
	ReturnReason     string    `json:"return_reason"` // AC03, AM04, etc.
	ReturnReasonDesc string    `json:"return_reason_desc"`
	ReturnedAt       time.Time `json:"returned_at"`
	AmountMinor      int64     `json:"amount_minor"`
}

// Adapter implements the SEPA SCT payment provider.
type Adapter struct {
	config         Config
	httpClient     *http.Client
	store          Store
	fundingService FundingService
	logger         *slog.Logger
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
	MarkRecalled(ctx context.Context, msgID, pmtInfID string, recallRef string, reason SEPARecallReason, additionalInfo string, recalledAt time.Time) error
	MarkReturned(ctx context.Context, msgID, pmtInfID string, returnReason string, returnedAt time.Time) error
	GetPendingPayments(ctx context.Context, olderThan time.Duration, limit int) ([]*SEPAPayment, error)
}

// FundingService callback interface.
type FundingService interface {
	ProcessInboundCredit(ctx context.Context, event *funding.InboundCreditEvent) error
	ProcessChargeback(ctx context.Context, intentID, reason string) error
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

// SetFundingService sets the funding service callback.
func (a *Adapter) SetFundingService(svc FundingService) {
	a.fundingService = svc
}

// Submit implements SEPAProvider.Submit - submits a payment to SEPA for funding.
// Returns a composite provider reference (msg_id:pmt_inf_id).
func (a *Adapter) Submit(ctx context.Context, intent *funding.FundingIntent, attemptID string) (providerRef string, err error) {
	// Generate SEPA identifiers
	msgID := fmt.Sprintf("MSG%s", ulid.Make().String())
	pmtInfID := fmt.Sprintf("PMT%s", ulid.Make().String())
	endToEndID := fmt.Sprintf("E2E%s", ulid.Make().String())

	// Get bank details from intent
	var iban, bic string
	if intent.BankDetails != nil {
		iban = intent.BankDetails.IBAN
		bic = intent.BankDetails.BIC
	}

	req := SubmitRequest{
		MsgID:        msgID,
		PmtInfID:     pmtInfID,
		EndToEndID:   endToEndID,
		Amount:       intent.Amount.AmountMinor,
		Currency:     string(intent.Amount.Currency),
		CreditorName: intent.CustomerID, // Would come from customer lookup
		CreditorIBAN: iban,
		CreditorBIC:  bic,
		Reference:    intent.BankDetails.Reference,
		IntentID:     intent.ID,
	}

	a.logger.Info("submitting SEPA payment",
		"intent_id", intent.ID,
		"msg_id", msgID,
		"pmt_inf_id", pmtInfID,
		"amount", intent.Amount.AmountMinor,
	)

	// Create SEPA payment record
	sepaPayment := &SEPAPayment{
		ID:               ulid.Make().String(),
		PaymentAttemptID: attemptID,
		IntentID:         intent.ID,
		MsgID:            msgID,
		PmtInfID:         pmtInfID,
		EndToEndID:       endToEndID,
		IBAN:             iban,
		BIC:              bic,
		AmountMinor:      intent.Amount.AmountMinor,
		Currency:         string(intent.Amount.Currency),
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
		"intent_id", intent.ID,
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

// GetStatus implements SEPAProvider.GetStatus - retrieves the status of a SEPA payment.
// providerRef is expected to be "msg_id:pmt_inf_id".
func (a *Adapter) GetStatus(ctx context.Context, providerRef string) (status string, settledAt *time.Time, err error) {
	parts := strings.SplitN(providerRef, ":", 2)
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid provider ref format, expected msg_id:pmt_inf_id")
	}
	msgID, pmtInfID := parts[0], parts[1]

	url := fmt.Sprintf("%s/payments/%s/%s", a.config.BaseURL, msgID, pmtInfID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return "", nil, fmt.Errorf("sepa api error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp StatusResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return resp.Status, resp.SettledAt, nil
}

// Recall initiates a recall for a SEPA payment.
// SEPA SCT Recall has a 10-day window from settlement.
func (a *Adapter) Recall(ctx context.Context, msgID, pmtInfID string, reason SEPARecallReason, additionalInfo string) (*RecallResponse, error) {
	// Get the payment to verify it can be recalled
	payment, err := a.store.GetByMsgAndPmtInf(ctx, msgID, pmtInfID)
	if err != nil {
		return nil, fmt.Errorf("get payment: %w", err)
	}

	if payment.Status != SEPASettled && payment.Status != SEPAAccepted {
		return nil, fmt.Errorf("can only recall settled/accepted payments, current status: %s", payment.Status)
	}

	// Check recall window (SEPA SCT Recall has 10-day window)
	if payment.SettledAt != nil && time.Since(*payment.SettledAt) > 10*24*time.Hour {
		return nil, fmt.Errorf("recall window expired (settled at %s)", payment.SettledAt)
	}

	a.logger.Info("initiating SEPA recall",
		"msg_id", msgID,
		"pmt_inf_id", pmtInfID,
		"reason", reason,
	)

	req := RecallRequest{
		MsgID:          msgID,
		PmtInfID:       pmtInfID,
		Reason:         reason,
		AdditionalInfo: additionalInfo,
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.BaseURL+"/payments/recall", bytes.NewReader(body))
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
		return nil, fmt.Errorf("sepa recall error: status=%d body=%s", httpResp.StatusCode, string(respBody))
	}

	var resp RecallResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Update local record
	if resp.Status == "ACCEPTED" || resp.Status == "PENDING" {
		now := time.Now()
		if err := a.store.MarkRecalled(ctx, msgID, pmtInfID, resp.RecallRef, reason, additionalInfo, now); err != nil {
			a.logger.Error("failed to update recall status", "error", err)
		}
	}

	a.logger.Info("SEPA recall initiated",
		"msg_id", msgID,
		"pmt_inf_id", pmtInfID,
		"recall_ref", resp.RecallRef,
		"status", resp.Status,
	)

	return &resp, nil
}

// HandleReturn processes an inbound return notification.
func (a *Adapter) HandleReturn(ctx context.Context, notification *ReturnNotification) error {
	a.logger.Info("processing SEPA return",
		"original_msg_id", notification.OriginalMsgID,
		"original_pmt_inf_id", notification.OriginalPmtInfID,
		"return_reason", notification.ReturnReason,
	)

	// Get the original payment
	payment, err := a.store.GetByMsgAndPmtInf(ctx, notification.OriginalMsgID, notification.OriginalPmtInfID)
	if err != nil {
		return fmt.Errorf("get original payment: %w", err)
	}

	if payment.Status != SEPASettled && payment.Status != SEPARecalled {
		a.logger.Warn("unexpected return for payment",
			"msg_id", notification.OriginalMsgID,
			"pmt_inf_id", notification.OriginalPmtInfID,
			"current_status", payment.Status,
		)
	}

	// Mark as returned
	if err := a.store.MarkReturned(ctx, notification.OriginalMsgID, notification.OriginalPmtInfID, notification.ReturnReason, notification.ReturnedAt); err != nil {
		return fmt.Errorf("mark returned: %w", err)
	}

	// Notify funding service to reverse the ledger entry
	if a.fundingService != nil && payment.IntentID != "" {
		reason := fmt.Sprintf("SEPA Return: %s - %s", notification.ReturnReason, notification.ReturnReasonDesc)
		if err := a.fundingService.ProcessChargeback(ctx, payment.IntentID, reason); err != nil {
			a.logger.Error("failed to process return in funding service", "error", err)
		}
	}

	a.logger.Info("SEPA payment returned",
		"msg_id", notification.OriginalMsgID,
		"pmt_inf_id", notification.OriginalPmtInfID,
		"return_reason", notification.ReturnReason,
		"amount", notification.AmountMinor,
	)

	return nil
}

// ProviderName returns the provider name for this adapter.
func (a *Adapter) ProviderName() string {
	return "sepa"
}
