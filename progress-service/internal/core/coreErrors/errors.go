// Package coreErrors holds sentinel domain errors. HTTP mapping lives in the primary adapter.
package coreErrors

import "errors"

var (
	ErrNoActiveProgram            = errors.New("no_active_program")
	ErrProgramAlreadyActive       = errors.New("program_already_active")
	ErrProgramNotFound            = errors.New("program_not_found")
	ErrProgramNotPaused           = errors.New("program_not_paused")
	ErrCurrentProgramNotCompleted = errors.New("current_program_not_completed")
	ErrProgramNoLongerExists      = errors.New("program_no_longer_exists")
	ErrNoProgramForClassifier     = errors.New("no_program_for_classifier")
	ErrProgramCatalogUnavailable  = errors.New("program_catalog_unavailable")

	ErrLessonAlreadyCompleted = errors.New("lesson_already_completed")
	ErrLessonNotAccessible    = errors.New("lesson_not_accessible")
	ErrNoAnswerSubmitted      = errors.New("no_answer_submitted")

	ErrInvalidMediaType  = errors.New("invalid_media_type")
	ErrInvalidPayload    = errors.New("invalid_payload")
	ErrInvalidDifficulty = errors.New("invalid_difficulty")
	ErrSameDifficulty    = errors.New("same_difficulty")

	ErrConfirmationRequired   = errors.New("confirmation_required")
	ErrIdempotencyKeyRequired = errors.New("idempotency_key_required")
	ErrIdempotencyInProgress  = errors.New("request_in_progress")
	ErrInvalidIdempotencyKey  = errors.New("invalid_idempotency_key")

	ErrAccountNotFound = errors.New("account_not_found")

	ErrInvalidToken     = errors.New("invalid_token")
	ErrTokenRevoked     = errors.New("token_revoked")
	ErrJWKSUnavailable  = errors.New("jwks_unavailable")
	ErrAuthUnavailable  = errors.New("auth_unavailable")
	ErrForbidden        = errors.New("forbidden")
	ErrInvalidPathParam = errors.New("invalid_path_param")
	ErrInvalidQuery     = errors.New("invalid_query")

	ErrS3NotConfigured     = errors.New("s3_not_configured")
	ErrServiceUnauthorized = errors.New("service_unauthorized")
)
