package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"
)

// handleGetAccount -- GET /iam/accounts/:accountId
// Response: 200 { email, role, status, created_at, rev }
// Guards: ownership (token.sub == accountId) OR role == administrator
func handleGetAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handlePatchAccount -- PATCH /iam/accounts/:accountId
// Body: { role?, personalInfo? }
// Response: 200 { email, role, status, rev }
func handlePatchAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handleDeleteAccount -- DELETE /iam/accounts/:accountId
// Response: 204 No Content
// Guard: role == administrator
func handleDeleteAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handleAdminCreateSession -- POST /iam/accounts/:accountId/sessions
// Auth: role == administrator
// Body: { fingerprint }
// Response: 201 { access_token, session_id }
func handleAdminCreateSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handleListSessions -- GET /iam/accounts/:accountId/sessions
// Response: 200 [{ session_id, fingerprint, last_activity, created_at }]
func handleListSessions() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handleTerminateSession -- DELETE /iam/accounts/:accountId/sessions/:sessionId
// Response: 204 No Content
func handleTerminateSession(session inport.SessionOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		// authCtx := c.MustGet(middleware.AuthContextKey).(valobj.AuthContext)
		// session.AdminTerminateSession(ctx, accountId, sessionId, authCtx.AccountID())
		c.Status(http.StatusNotImplemented)
	}
}

// handleChangePassword -- POST /iam/accounts/:accountId/password
// Body: { current_password, new_password }
// Response: 200 { message }
// Side effects: rev++, all sessions terminated
func handleChangePassword() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handleLockAccount -- POST /iam/accounts/:accountId/lock
// Auth: role == administrator
// Body: { locked_until?: DateTime | null }
// Response: 200 { status: "blocked", locked_until }
func handleLockAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}

// handleUnlockAccount -- POST /iam/accounts/:accountId/unlock
// Auth: role == administrator
// Response: 200 { status: "active" }
func handleUnlockAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement
		c.Status(http.StatusNotImplemented)
	}
}
