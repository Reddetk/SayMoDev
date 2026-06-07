package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// respondErr is the final fallback for all use-case errors.
// Call only after all business-specific cases are exhausted in the handler switch.
// Business classifiers (isOTPError, isPasswordPolicyError, etc.) stay in handler switches.
func respondErr(c *gin.Context, err error) {
	switch {
	case isRateLimit(err):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
	case isInfraError(err):
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}

// respondInternalErr is used for handler-level failures (hashPassword, type assertions)
// where no use-case error is available for classification.
func respondInternalErr(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
