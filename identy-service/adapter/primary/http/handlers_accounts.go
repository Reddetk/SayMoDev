package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

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
			default:
				respondErr(c, err)
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

		// Business rule: только administrator может менять роль аккаунта.
		// Остаётся в handler, так как зависит от тела запроса, а не только от identity.
		if req.Role != nil && ac.Role != middleware.RoleAdministrator {
			c.JSON(http.StatusForbidden, gin.H{"error": "role change requires administrator"})
			return
		}

		current, err := accOp.AdminGetAccountData(c.Request.Context(), accountID)
		if err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			default:
				respondErr(c, err)
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
			default:
				respondErr(c, err)
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

		if err := accOp.SoftDelete(c.Request.Context(), accountID, ac.AccountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountAlreadyDeleted:
				c.JSON(http.StatusConflict, gin.H{"error": "account already deleted"})
			default:
				respondErr(c, err)
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
			default:
				respondErr(c, err)
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

		if ac.Role == middleware.RoleAdministrator && ac.AccountID != accountID {
			if err := sesOp.AdminTerminateSession(ctx, accountID, sessionID, ac.AccountID); err != nil {
				switch {
				case err == corerr.ErrSessionNotFound:
					// Idempotent -- session already gone.
				case err == corerr.ErrAccountNotFound:
					c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
					return
				default:
					respondErr(c, err)
					return
				}
			}
		} else {
			if err := sesOp.Logout(ctx, accountID, sessionID); err != nil {
				switch {
				case err == corerr.ErrSessionNotFound:
					// Idempotent.
				default:
					respondErr(c, err)
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
//
// Port contract (passwordOperator.go):
//   "Caller is responsible for verifying the current password before calling this method."
//
// Verification flow:
//  1. Fetch current account data to obtain stored password hash.
//  2. bcrypt.CompareHashAndPassword(storedHash, currentPassword) -- 401 on mismatch.
//  3. Hash newPassword, call PasswordChange.
//
// NOTE: missing spec -- whether administrator may bypass currentPassword check
// is not defined in BC#1. Until resolved, currentPassword is always required.
// ---------------------------------------------------------------------------

func handleChangePassword(accOp inport.AccountOperator, passOp inport.PasswordOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountID := c.Param("accountId")

		var req changePasswordReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Step 1: fetch stored hash to verify currentPassword.
		current, err := accOp.AdminGetAccountData(c.Request.Context(), accountID)
		if err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				// Anti-enumeration: 401, not 404.
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid current password"})
			case err == corerr.ErrFederatedAccountHasNoPassword:
				c.JSON(http.StatusConflict, gin.H{"error": "federated account has no password set"})
			default:
				respondErr(c, err)
			}
			return
		}

		// Federated account edge case
		if current.PasswordHash == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "federated account has no password set"})
			return
		}

		// Step 2: verify currentPassword against stored hash.
		// bcrypt.CompareHashAndPassword is constant-time.
		// TODO fix
		if err := bcrypt.CompareHashAndPassword([]byte(*current.PasswordHash), []byte(req.CurrentPassword)); err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid current password"})
				return
			}
			// bcrypt.ErrHashTooShort or unexpected error -- treat as infra failure.
			respondInternalErr(c)
			return
		}

		// Step 3: hash new password and delegate to use case.
		newPasswordHash, err := hashPassword(req.NewPassword)
		if err != nil {
			respondInternalErr(c)
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
			default:
				respondErr(c, err)
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
		_ = c.ShouldBindJSON(&req)

		if err := accOp.LockAccount(c.Request.Context(), accountID, req.LockedUntil, ac.AccountID); err != nil {
			switch {
			case err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			case err == corerr.ErrAccountAlreadyLocked:
				c.JSON(http.StatusConflict, gin.H{"error": "account is already locked"})
			case err == corerr.ErrAccountDeleted:
				c.JSON(http.StatusConflict, gin.H{"error": "account is deleted"})
			default:
				respondErr(c, err)
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
			default:
				respondErr(c, err)
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "active"})
	}
}
