// Package httpapi contains the primary Gin adapter: routing, middleware and handlers.
package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
)

// errorStatus maps domain sentinels to the documented HTTP contract.
func errorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, corerr.ErrNoActiveProgram),
		errors.Is(err, corerr.ErrAccountNotFound),
		errors.Is(err, corerr.ErrProgramNotPaused),
		errors.Is(err, corerr.ErrProgramNoLongerExists),
		errors.Is(err, corerr.ErrProgramNotFound),
		errors.Is(err, corerr.ErrNoProgramForClassifier):
		return http.StatusNotFound, err.Error()

	case errors.Is(err, corerr.ErrProgramAlreadyActive),
		errors.Is(err, corerr.ErrLessonAlreadyCompleted),
		errors.Is(err, corerr.ErrIdempotencyInProgress):
		return http.StatusConflict, err.Error()

	case errors.Is(err, corerr.ErrLessonNotAccessible):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, corerr.ErrForbidden):
		return http.StatusForbidden, "forbidden"

	case errors.Is(err, corerr.ErrNoAnswerSubmitted),
		errors.Is(err, corerr.ErrInvalidMediaType),
		errors.Is(err, corerr.ErrInvalidPayload):
		return http.StatusUnprocessableEntity, err.Error()

	case errors.Is(err, corerr.ErrInvalidDifficulty),
		errors.Is(err, corerr.ErrSameDifficulty),
		errors.Is(err, corerr.ErrCurrentProgramNotCompleted),
		errors.Is(err, corerr.ErrConfirmationRequired),
		errors.Is(err, corerr.ErrInvalidPathParam),
		errors.Is(err, corerr.ErrInvalidQuery),
		errors.Is(err, corerr.ErrIdempotencyKeyRequired),
		errors.Is(err, corerr.ErrInvalidIdempotencyKey):
		return http.StatusBadRequest, err.Error()

	case errors.Is(err, corerr.ErrProgramCatalogUnavailable),
		errors.Is(err, corerr.ErrJWKSUnavailable),
		errors.Is(err, corerr.ErrAuthUnavailable),
		errors.Is(err, corerr.ErrS3NotConfigured):
		return http.StatusServiceUnavailable, err.Error()

	case errors.Is(err, corerr.ErrInvalidToken),
		errors.Is(err, corerr.ErrTokenRevoked),
		errors.Is(err, corerr.ErrServiceUnauthorized):
		return http.StatusUnauthorized, "invalid_token"
	}
	return http.StatusInternalServerError, "internal_error"
}

func fail(c *gin.Context, err error) {
	status, code := errorStatus(err)
	if status >= 500 {
		c.Error(err) //nolint:errcheck // collected by the access log middleware
	}
	c.AbortWithStatusJSON(status, gin.H{"error": code})
}

func badRequest(c *gin.Context, code string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": code})
}
