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
// GET /iam/accounts/:accountId
// Response: 200 AccountDTO
// Guard: OwnershipOrAdmin middleware (applied at router group level)
// ---------------------------------------------------------------------------

func handleGetAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountID := c.Param("accountId")

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
// Guard: OwnershipOrAdmin middleware (applied at router group level)
// Business rule: role change is administrator-only (checked here, not in middleware)
// ---------------------------------------------------------------------------

func handlePatchAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := middleware.MustGetAuthContext(c)
		accountID := c.Param("accountId")

		var req patchAccountReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Business rule: only administrator may change account role.
		// This is intentionally kept in the handler (not middleware) because
		// it depends on the request body, not just identity.
		if req.Role != nil && ac.Role != inport.RoleAdministrator {
			c.JSON(http.StatusForbidden, gin.H{"error": "role change requires administrator"})
			return
		}

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
// Guard: RequireRole(administrator) middleware (applied at router route level)
// Side effects: T4 mass-revoke, AccountDeleted event -> BC#2, BC#4 cascade.
// ---------------------------------------------------------------------------

func handleDeleteAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := middleware.MustGetAuthContext(c)
		accountID := c.Param("accountId")

		// actorID sourced from JWT claims -- never from request body (§ Audit).
		if err := accOp.SoftDelete(c.Request.Context(), accountID, ac.AccountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountAlreadyDeleted:
				c.JSON(http.StatusConflict, gin.H{"error": "account already deleted"})
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
// Guard: OwnershipOrAdmin middleware (applied at router group level)
// ---------------------------------------------------------------------------

func handleListSessions(sesOp inport.SessionOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountID := c.Param("accountId")

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
// Guard: OwnershipOrAdmin middleware (applied at router group level)
// Business logic: administrator uses AdminTerminateSession (audit trail),
// owner uses Logout (self-service path). Branching is intentional here.
// ---------------------------------------------------------------------------

func handleTerminateSession(sesOp inport.SessionOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := middleware.MustGetAuthContext(c)
		accountID := c.Param("accountId")
		sessionID := c.Param("sessionId")

		ctx := c.Request.Context()

		if ac.Role == inport.RoleAdministrator && ac.AccountID != accountID {
			// Administrator terminating another account's session (audit trail path).
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
// Guard: OwnershipOrAdmin middleware (applied at router group level)
// NOTE: missing spec -- whether administrator may change another account's password
// is not defined in BC#1. Until resolved, no additional gate is applied here.
// ---------------------------------------------------------------------------

func handleChangePassword(passOp inport.PasswordOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountID := c.Param("accountId")

		var req changePasswordReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

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
// Guard: RequireRole(administrator) middleware (applied at router route level)
// Side effects: §6 Lock Semantics -- rev++, all jti blacklisted, sessions deleted atomically.
// ---------------------------------------------------------------------------

func handleLockAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := middleware.MustGetAuthContext(c)
		accountID := c.Param("accountId")

		var req lockAccountReq
		// Body is optional -- lock without expiry is valid.
		_ = c.ShouldBindJSON(&req)

		// actorID from JWT claims -- never from request body (§ Audit).
		if err := accOp.LockAccount(c.Request.Context(), accountID, req.LockedUntil, ac.AccountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountAlreadyLocked:
				c.JSON(http.StatusConflict, gin.H{"error": "account is already locked"})
			case err == corerr.ErrAccountDeleted:
				c.JSON(http.StatusConflict, gin.H{"error": "account is deleted"})
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
// Guard: RequireRole(administrator) middleware (applied at router route level)
// ---------------------------------------------------------------------------

func handleUnlockAccount(accOp inport.AccountOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountID := c.Param("accountId")

		if err := accOp.UnlockAccount(c.Request.Context(), accountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountNotLocked:
				c.JSON(http.StatusConflict, gin.H{"error": "account is not locked"})
			case err == corerr.ErrAccountDeleted:
				c.JSON(http.StatusConflict, gin.H{"error": "account is deleted"})
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
