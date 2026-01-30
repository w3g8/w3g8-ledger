package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/oklog/ulid/v2"
)

// Context keys
type contextKey string

const (
	CorrelationIDKey contextKey = "correlation_id"
	TenantIDKey      contextKey = "tenant_id"
	UserIDKey        contextKey = "user_id"
	RequestIDKey     contextKey = "request_id"
)

// GetCorrelationID retrieves the correlation ID from context
func GetCorrelationID(ctx context.Context) string {
	if v, ok := ctx.Value(CorrelationIDKey).(string); ok {
		return v
	}
	return ""
}

// GetTenantID retrieves the tenant ID from context
func GetTenantID(ctx context.Context) string {
	if v, ok := ctx.Value(TenantIDKey).(string); ok {
		return v
	}
	return ""
}

// GetUserID retrieves the user ID from context
func GetUserID(ctx context.Context) string {
	if v, ok := ctx.Value(UserIDKey).(string); ok {
		return v
	}
	return ""
}

// CorrelationID middleware adds a correlation ID to each request
func CorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = ulid.Make().String()
		}

		ctx := context.WithValue(r.Context(), CorrelationIDKey, correlationID)
		w.Header().Set("X-Correlation-ID", correlationID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestID middleware adds a request ID to each request
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := ulid.Make().String()
		ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logger creates a structured logging middleware
func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				logger.Info("request completed",
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration_ms", time.Since(start).Milliseconds(),
					"correlation_id", GetCorrelationID(r.Context()),
					"tenant_id", GetTenantID(r.Context()),
					"user_agent", r.UserAgent(),
					"remote_addr", r.RemoteAddr,
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// Recoverer recovers from panics and logs them
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"panic", rec,
						"stack", string(debug.Stack()),
						"path", r.URL.Path,
						"method", r.Method,
						"correlation_id", GetCorrelationID(r.Context()),
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"error": map[string]string{
							"code":    "INTERNAL_ERROR",
							"message": "An unexpected error occurred",
						},
					})
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// TenantExtractor extracts tenant ID from header or path
func TenantExtractor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			// Try to extract from path (e.g., /tenants/{tenant_id}/...)
			// This is a simple implementation; adjust based on your routing
		}

		if tenantID != "" {
			ctx := context.WithValue(r.Context(), TenantIDKey, tenantID)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// APIKeyAuth validates API key authentication
type APIKeyValidator func(ctx context.Context, apiKey string) (tenantID, userID string, err error)

func APIKeyAuth(validator APIKeyValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing authorization header")
				return
			}

			// Support both "Bearer <key>" and "ApiKey <key>"
			var apiKey string
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimPrefix(authHeader, "Bearer ")
			} else if strings.HasPrefix(authHeader, "ApiKey ") {
				apiKey = strings.TrimPrefix(authHeader, "ApiKey ")
			} else {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid authorization format")
				return
			}

			tenantID, userID, err := validator(r.Context(), apiKey)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid API key")
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, TenantIDKey, tenantID)
			ctx = context.WithValue(ctx, UserIDKey, userID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireTenant ensures a tenant ID is present
func RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetTenantID(r.Context()) == "" {
			writeError(w, http.StatusBadRequest, "MISSING_TENANT", "Tenant ID is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IdempotencyKey provides idempotency handling
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (response []byte, found bool, err error)
	Set(ctx context.Context, key string, response []byte, ttl time.Duration) error
}

func Idempotency(store IdempotencyStore, ttl time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only apply to mutating methods
			if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
				next.ServeHTTP(w, r)
				return
			}

			idempotencyKey := r.Header.Get("Idempotency-Key")
			if idempotencyKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Check if we have a cached response
			cached, found, err := store.Get(r.Context(), idempotencyKey)
			if err != nil {
				// Log error but continue with request
				next.ServeHTTP(w, r)
				return
			}

			if found {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotency-Replayed", "true")
				_, _ = w.Write(cached)
				return
			}

			// Capture the response
			rec := &responseRecorder{ResponseWriter: w, body: make([]byte, 0)}
			next.ServeHTTP(rec, r)

			// Store successful responses
			if rec.status >= 200 && rec.status < 300 {
				_ = store.Set(r.Context(), idempotencyKey, rec.body, ttl)
			}
		})
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}

// CORS middleware
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			allowed := false
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Correlation-ID, X-Tenant-ID, Idempotency-Key")
				w.Header().Set("Access-Control-Expose-Headers", "X-Correlation-ID, X-Request-ID")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit provides basic rate limiting
// For production, use a distributed rate limiter like Redis
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

func RateLimit(limiter RateLimiter, keyFunc func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			allowed, err := limiter.Allow(r.Context(), key)
			if err != nil {
				// Log error but allow request on limiter failure
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ContentType sets the content type header
func ContentType(contentType string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			next.ServeHTTP(w, r)
		})
	}
}

// JSON is a convenience for ContentType("application/json")
func JSON(next http.Handler) http.Handler {
	return ContentType("application/json")(next)
}

// writeError writes a JSON error response
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
