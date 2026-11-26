package httpapi

// goca:manual — idempotency middleware BC#2 (T1[1.3], Redis L2 + PostgreSQL L3).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

type cachedResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// IdempotencyMiddleware кэширует результат мутирующей команды по Idempotency-Key.
func IdempotencyMiddleware(store out.Store, cache out.Cache, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			if required {
				abortJSON(c, http.StatusBadRequest, "invalid_input", "Idempotency-Key is required")
				return
			}
			c.Next()
			return
		}
		if _, err := uuid.Parse(key); err != nil {
			abortJSON(c, http.StatusBadRequest, "invalid_input", "Idempotency-Key must be a UUID")
			return
		}
		authCtx := authFrom(c)
		if authCtx.AccountID == "" {
			abortJSON(c, http.StatusUnauthorized, "unauthorized", "missing auth context")
			return
		}

		cacheKey := "idem:" + authCtx.AccountID + ":" + c.FullPath() + ":" + key
		if payload, err := cache.Get(c.Request.Context(), cacheKey); err == nil {
			replay(c, payload)
			return
		}

		record, err := store.GetIdempotency(c.Request.Context(), authCtx.AccountID, key)
		if err == nil {
			if record.Endpoint != c.FullPath() {
				abortJSON(c, http.StatusConflict, "idempotency_in_progress", "key was used for another endpoint")
				return
			}
			if record.Status == "completed" {
				payload, _ := json.Marshal(cachedResponse{Status: record.ResponseCode, Body: record.ResponseBody})
				_ = cache.Set(c.Request.Context(), cacheKey, payload, 24*time.Hour)
				replay(c, payload)
				return
			}
			abortJSON(c, http.StatusConflict, "idempotency_in_progress", "request is already in progress")
			return
		}

		created, err := store.CreateIdempotency(c.Request.Context(), out.IdempotencyRecord{
			Key: key, AccountID: authCtx.AccountID, Endpoint: c.FullPath(),
		})
		if err != nil {
			abortJSON(c, http.StatusInternalServerError, "internal_error", "idempotency store error")
			return
		}
		if !created {
			abortJSON(c, http.StatusConflict, "idempotency_in_progress", "request is already in progress")
			return
		}

		writer := &bufferedWriter{ResponseWriter: c.Writer, buffer: &bytes.Buffer{}}
		c.Writer = writer
		c.Next()

		body := writer.buffer.Bytes()
		if len(body) == 0 {
			body = []byte("{}")
		}
		if writer.Status() >= 400 {
			_ = store.DeleteIdempotency(c.Request.Context(), authCtx.AccountID, key)
			return
		}
		_ = store.CompleteIdempotency(c.Request.Context(), out.IdempotencyRecord{
			Key: key, AccountID: authCtx.AccountID, Status: "completed",
			ResponseCode: writer.Status(), ResponseBody: body,
		})
		payload, _ := json.Marshal(cachedResponse{Status: writer.Status(), Body: body})
		_ = cache.Set(c.Request.Context(), cacheKey, payload, 24*time.Hour)
	}
}

func replay(c *gin.Context, payload []byte) {
	var response cachedResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		abortJSON(c, http.StatusInternalServerError, "internal_error", "idempotency cache is corrupted")
		return
	}
	c.Data(response.Status, "application/json; charset=utf-8", response.Body)
	c.Abort()
}

// bufferedWriter запоминает тело ответа, не мешая gin.
type bufferedWriter struct {
	gin.ResponseWriter
	buffer *bytes.Buffer
	status int
}

func (w *bufferedWriter) Write(data []byte) (int, error) {
	w.buffer.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *bufferedWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *bufferedWriter) WriteString(value string) (int, error) {
	w.buffer.WriteString(value)
	return w.ResponseWriter.WriteString(value)
}

func (w *bufferedWriter) Status() int {
	if w.status != 0 {
		return w.status
	}
	return w.ResponseWriter.Status()
}
