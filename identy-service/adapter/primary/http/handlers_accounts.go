package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http/middleware"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"
)

// ---------------------------------------------------------------------------
// Request / response types (account handlers only)
// ---------------------------------------------------------------------------

type patchAccountReq struct {
	Role         *string `json:"role"`
	PersonalInfo *string `json:"personalInfo"`
}

type lockAccountReq struct {
	// LockedUntil is a Unix timestamp (seconds). Null means indefinite lock.
	LockedUntil *int64 `json:"locked_until"`
}

type changePasswordReq struct {
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewPassword     string `json:"newPassword"     binding:"required,min=8,max=72"`
}

// ---------------------------------------------------------------------------
// Helper: extract AuthContext from Gin context (set by JWTMiddleware).
// ---------------------------------------------------------------------------

func mustAuthContext(c *gin.Context) (inport.AuthContext, bool) {
	raw, ok := c.Get(middleware.AuthContextKey)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing auth context"})
		return inport.AuthContext{}, false
	}
	ac, ok := raw.(inport.AuthContext)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return inport.AuthContext{}, false
	}
	return ac, true
}

// ---------------------------------------------------------------------------
// GET /iam/accounts/:accountId
// Response: 200 AccountDTO
// Guard: ownership (token.sub == accountId) OR role == administrator
// ---------------------------------------------------------------------------

func handleGetAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		accountID := c.Param("accountId")

		// Ownership OR admin check.
		if ac.AccountID != accountID && ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		dto, err := accOp.AdminGetAccountData(c.Request.Context(), accountID)
		if err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":           dto.ID,
			"email":        dto.Email,
			"role":         dto.Role,
			"status":       dto.Status,
			"personalInfo": dto.PersonalInfo,
			"lockedUntil":  dto.LockedUntil,
			"metadata":     dto.Metadata,
		})
	}
}

// ---------------------------------------------------------------------------
// PATCH /iam/accounts/:accountId
// Body: { role?, personalInfo? }
// Response: 200 AccountDTO
// Guard: ownership (token.sub == accountId) OR role == administrator
// ---------------------------------------------------------------------------

func handlePatchAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		accountID := c.Param("accountId")

		// Role change is administrator-only; personalInfo update is ownership-allowed.
		if ac.AccountID != accountID && ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		var req patchAccountReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Non-admin cannot change role.
		if req.Role != nil && ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "role change requires administrator"})
			return
		}

		// Fetch current state to build patched DTO.
		current, err := accOp.AdminGetAccountData(c.Request.Context(), accountID)
		if err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		if req.Role != nil {
			current.Role = *req.Role
		}
		if req.PersonalInfo != nil {
			current.PersonalInfo = *req.PersonalInfo
		}

		if err := accOp.AdminChangeAccountData(c.Request.Context(), current); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrInvalidRole:
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":           current.ID,
			"email":        current.Email,
			"role":         current.Role,
			"status":       current.Status,
			"personalInfo": current.PersonalInfo,
			"lockedUntil":  current.LockedUntil,
			"metadata":     current.Metadata,
		})
	}
}

// ---------------------------------------------------------------------------
// DELETE /iam/accounts/:accountId
// Response: 204 No Content
// Guard: role == administrator
// Side effects: T4 mass-revoke, AccountDeleted event -> BC#2, BC#4 cascade.
// ---------------------------------------------------------------------------

func handleDeleteAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		if ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		accountID := c.Param("accountId")

		// actorID sourced from JWT claims -- never from request body (§ Audit).
		if err := accOp.SoftDelete(c.Request.Context(), accountID, ac.AccountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// GET /iam/accounts/:accountId/sessions
// Response: 200 [SessionDTO]
// Guard: ownership (token.sub == accountId) OR role == administrator
// ---------------------------------------------------------------------------

func handleListSessions(sesOp inport.SessionOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		accountID := c.Param("accountId")

		if ac.AccountID != accountID && ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		sessions, err := sesOp.AdminGetSessions(c.Request.Context(), accountID)
		if err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, sessions)
	}
}

// ---------------------------------------------------------------------------
// DELETE /iam/accounts/:accountId/sessions/:sessionId
// Response: 204 No Content
// Guard: ownership OR role == administrator
// ---------------------------------------------------------------------------

func handleTerminateSession(sesOp inport.SessionOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		accountID := c.Param("accountId")
		sessionID := c.Param("sessionId")

		isOwner := ac.AccountID == accountID
		isAdmin := ac.Role == "administrator"

		if !isOwner && !isAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		ctx := c.Request.Context()

		if isAdmin && !isOwner {
			// Administrator terminating another account's session.
			if err := sesOp.AdminTerminateSession(ctx, accountID, sessionID, ac.AccountID); err != nil {
				switch {
				case err == corerr.ErrSessionNotFound:
					// Idempotent -- session already gone.
				case err == corerr.ErrAccountNotFound:
					c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
					return
				case isInfraError(err):
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
			}
		} else {
			// Owner terminating own session (self-logout path for a specific session).
			if err := sesOp.Logout(ctx, accountID, sessionID); err != nil {
				switch {
				case err == corerr.ErrSessionNotFound:
					// Idempotent.
				case isInfraError(err):
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
			}
		}

		c.Status(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// POST /iam/accounts/:accountId/password
// Body: { currentPassword, newPassword }
// Response: 204 No Content
// Guard: ownership (token.sub == accountId)
// Side effects: T4 mass-revoke -- rev++, all sessions cleared, AccessTokenRevoked events.
// Caller is responsible for verifying currentPassword before delegating to PasswordChange (port/in contract).
// ---------------------------------------------------------------------------

func handleChangePassword(passOp inport.PasswordOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		accountID := c.Param("accountId")

		// Only the account owner may change their own password.
		if ac.AccountID != accountID {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		var req changePasswordReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// port/in PasswordChange contract: caller must hash the new password.
		newPasswordHash, err := hashPassword(req.NewPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		if err := passOp.PasswordChange(
			c.Request.Context(),
			accountID,
			newPasswordHash,
		); err != nil {
			switch {
			case err == corerr.ErrInvalidCredentials:
				// Current password verification failed inside the service.
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid current password"})
			case isPasswordPolicyError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "password policy violation"})
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// POST /iam/accounts/:accountId/lock
// Body: { locked_until?: int64 | null }
// Response: 200 { status, locked_until }
// Guard: role == administrator
// Side effects: §6 Lock Semantics -- rev++, all jti blacklisted, sessions deleted atomically.
// ---------------------------------------------------------------------------

func handleLockAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		if ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		accountID := c.Param("accountId")

		var req lockAccountReq
		// Body is optional -- lock without expiry is valid.
		_ = c.ShouldBindJSON(&req)

		// actorID from JWT claims -- never from request body (§ Audit).
		if err := accOp.LockAccount(c.Request.Context(), accountID, req.LockedUntil, ac.AccountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountLocked:
				c.JSON(http.StatusConflict, gin.H{"error": "account already locked"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":       "blocked",
			"locked_until": req.LockedUntil,
		})
	}
}

// ---------------------------------------------------------------------------
// POST /iam/accounts/:accountId/unlock
// Response: 200 { status }
// Guard: role == administrator
// ---------------------------------------------------------------------------

func handleUnlockAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := mustAuthContext(c)
		if !ok {
			return
		}

		if ac.Role != "administrator" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		accountID := c.Param("accountId")

		if err := accOp.UnlockAccount(c.Request.Context(), accountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountNotActive: // TODO some shit with Locking acc
				c.JSON(http.StatusConflict, gin.H{"error": "account is not locked"})
			case isInfraError(err):
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "active"})
	}
}

// ---------------------------------------------------------------------------
// NOTE: handleAdminCreateSession stub -- removed from this file.
// POST /iam/accounts/:accountId/sessions is not backed by a port/in method
// in the current version of SessionOperator. This is a missing spec / design gap.
// When AdminCreateSession is added to port/in, the handler should be wired here.
// ---------------------------------------------------------------------------
