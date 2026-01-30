package fps

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"finplatform/internal/domain"
	"finplatform/internal/events"
)

// WebhookPayload is the structure of FPS webhook callbacks.
type WebhookPayload struct {
	EndToEndID        string `json:"end_to_end_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	Status            string `json:"status"` // ACCEPTED, SETTLED, FAILED
	SettledAt         string `json:"settled_at,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"error_message,omitempty"`
	Timestamp         string `json:"timestamp"`
}

// EventPublisher publishes events to NATS.
type EventPublisher interface {
	Publish(ctx interface{}, subject string, env *events.Envelope) error
}

// WebhookHandler handles FPS webhook callbacks.
type WebhookHandler struct {
	store     Store
	publisher EventPublisher
	logger    *slog.Logger
}

// NewWebhookHandler creates a new FPS webhook handler.
func NewWebhookHandler(store Store, publisher EventPublisher, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		store:     store,
		publisher: publisher,
		logger:    logger,
	}
}

// ServeHTTP handles incoming FPS webhook requests.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("failed to read webhook body", "error", err)
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("failed to parse webhook payload", "error", err, "body", string(body))
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	h.logger.Info("received FPS webhook",
		"end_to_end_id", payload.EndToEndID,
		"status", payload.Status,
	)

	// Look up the FPS payment
	fpsPayment, err := h.store.GetByEndToEndID(ctx, payload.EndToEndID)
	if err != nil {
		h.logger.Error("fps payment not found", "end_to_end_id", payload.EndToEndID, "error", err)
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}

	// Process based on status
	switch payload.Status {
	case "ACCEPTED":
		h.handleAccepted(r.Context(), fpsPayment, payload)
	case "SETTLED":
		h.handleSettled(r.Context(), fpsPayment, payload)
	case "FAILED":
		h.handleFailed(r.Context(), fpsPayment, payload)
	default:
		h.logger.Warn("unknown FPS status", "status", payload.Status)
	}

	// Acknowledge the webhook
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *WebhookHandler) handleAccepted(ctx context.Context, fpsPayment *FPSPayment, payload WebhookPayload) {
	acceptedAt := time.Now()

	pgStore, ok := h.store.(*PostgresStore)
	if ok {
		if err := pgStore.MarkAccepted(ctx, fpsPayment.EndToEndID, acceptedAt); err != nil {
			h.logger.Error("failed to mark fps payment accepted", "error", err)
		}
	}

	h.logger.Info("FPS payment accepted",
		"end_to_end_id", fpsPayment.EndToEndID,
		"payment_attempt_id", fpsPayment.PaymentAttemptID,
	)
}

func (h *WebhookHandler) handleSettled(ctx context.Context, fpsPayment *FPSPayment, payload WebhookPayload) {
	settledAt := time.Now()
	if payload.SettledAt != "" {
		if t, err := time.Parse(time.RFC3339, payload.SettledAt); err == nil {
			settledAt = t
		}
	}

	// Mark FPS payment as settled
	if err := h.store.MarkSettled(ctx, fpsPayment.EndToEndID, settledAt); err != nil {
		h.logger.Error("failed to mark fps payment settled", "error", err)
		return
	}

	h.logger.Info("FPS payment settled",
		"end_to_end_id", fpsPayment.EndToEndID,
		"payment_attempt_id", fpsPayment.PaymentAttemptID,
	)

	// Publish provider settlement event to trigger settlement handler
	h.publishSettlement(ctx, fpsPayment, "SETTLED", "", "", settledAt)
}

func (h *WebhookHandler) handleFailed(ctx context.Context, fpsPayment *FPSPayment, payload WebhookPayload) {
	// Mark FPS payment as failed
	if err := h.store.MarkFailed(ctx, fpsPayment.EndToEndID, payload.ErrorCode, payload.ErrorMessage); err != nil {
		h.logger.Error("failed to mark fps payment failed", "error", err)
		return
	}

	h.logger.Info("FPS payment failed",
		"end_to_end_id", fpsPayment.EndToEndID,
		"payment_attempt_id", fpsPayment.PaymentAttemptID,
		"error_code", payload.ErrorCode,
	)

	// Publish provider settlement event to trigger settlement handler
	h.publishSettlement(ctx, fpsPayment, "FAILED", payload.ErrorCode, payload.ErrorMessage, time.Now())
}

func (h *WebhookHandler) publishSettlement(ctx context.Context, fpsPayment *FPSPayment, status, errorCode, errorMsg string, settledAt time.Time) {
	if h.publisher == nil {
		return
	}

	settlement := events.ProviderSettlement{
		Provider:    "fps",
		ProviderRef: fpsPayment.EndToEndID,
		Status:      status,
		ErrorCode:   errorCode,
		ErrorMsg:    errorMsg,
		SettledAt:   settledAt,
	}

	// Create envelope with a placeholder tenant ID (will be looked up by settlement handler)
	env, err := events.NewEnvelope("provider.settlement.v1", domain.TenantID(""), fpsPayment.PaymentAttemptID, &settlement)
	if err != nil {
		h.logger.Error("failed to create settlement envelope", "error", err)
		return
	}

	// Publish to the provider settlement subject
	if err := h.publisher.Publish(ctx, "provider.settlement", env); err != nil {
		h.logger.Error("failed to publish settlement event", "error", err)
	}
}
