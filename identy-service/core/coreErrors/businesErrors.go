// Package corerr - conatain errors of core
package corerr

import "errors"

// Busines errors
var (
	ErrUserOTPisNotCorrect = errors.New("otp is not correct")
	ErrUserOTPisNotValid   = errors.New("otp is not valid")
	ErrEmailAlreadyExists  = errors.New("email already exists")
)

// adaptererrors
var (
	ErrEmailDeliveryFailed = errors.New("email selivery failed")
	ErrOTPRepository       = errors.New("otp rep req is failed")
	ErrAccountRepository   = errors.New("acc rep is failed")
)

// Validation errors
var (
	ErrSessionNotFound                          = errors.New("session not found")
	ErrInvalidEmail                             = errors.New("invalid email format")
	ErrEmailTooLong                             = errors.New("email too long")
	ErrEmailRequired                            = errors.New("email is required")
	ErrPasswordTooShort                         = errors.New("password too short (minimum 8 characters)")
	ErrPasswordTooLong                          = errors.New("password too long (bcrypt limit: 72 bytes)")
	ErrInvalidRole                              = errors.New("invalid role")
	ErrRoleRequired                             = errors.New("role is required")
	ErrInvalidStatus                            = errors.New("invalid status")
	ErrStatusRequired                           = errors.New("status is required")
	ErrInvalidRevision                          = errors.New("revision cannot be negative")
	ErrInvalidLockedUntil                       = errors.New("locked until cannot be in the past for active accounts")
	ErrEmptyPersonalInfo                        = errors.New("personal info cannot be empty")
	ErrPersonalInfoTooLong                      = errors.New("personal info is too long")
	ErrPersonalInfoRequired                     = errors.New("personal info is required")
	ErrInvalidClassifier                        = errors.New("invalid classifier")
	ErrInvalidSessionID                         = errors.New("invalid session ID")
	ErrInvalidJTI                               = errors.New("invalid JTI format")
	ErrInvalidFingerprint                       = errors.New("invalid fingerprint")
	ErrInvalidLastActivity                      = errors.New("invalid last activity time")
	ErrInvalidPasswordHash                      = errors.New("invalid password hash")
	ErrInvalidPasswordHashLength                = errors.New("invalid password hash length")
	ErrInvalidPasswordHashFormat                = errors.New("invalid bcrypt hash format")
	ErrPasswordHistoryFull                      = errors.New("password history is limited to 5 entries")
	ErrAccountRequiresAuth                      = errors.New("account must have either google uid or password hash")
	ErrAccountCannotHaveBoth                    = errors.New("account cannot have both google uid and password hash")
	ErrGoogleUIDCannotBeEmpty                   = errors.New("google uid cannot be empty")
	ErrGoogleUIDTooLong                         = errors.New("google uid too long")
	ErrPasswordHashCannotBeEmpty                = errors.New("password hash cannot be empty")
	ErrOAuthAccountMustHaveNullPasswordHash     = errors.New("OAuth accounts must have null password hash")
	ErrEmailPasswordAccountMustHavePasswordHash = errors.New("email/password accounts must have password hash")
	ErrActiveAccountCannotBeLockedInFuture      = errors.New("active account cannot be locked in future")
	ErrLockedUntilNegative                      = errors.New("lockedUntil cannot be negative")
	ErrAccountDeleted                           = errors.New("account deleted")
	ErrAccountNotActive                         = errors.New("account not active")
	ErrAccountAlreadyDeleted                    = errors.New("account already deleted")
	ErrOTPAlreadyExpired                        = errors.New("otp hash already expired")
	ErrOTPHashRequired                          = errors.New("otp hash req")
	ErrOTPHashInvalidLength                     = errors.New("otp hash invalid length")
	ErrVerificationCodeIDRequired               = errors.New("verf code id req")
)

// otp_purpooose
var (
	ErrInvalidOTPPurpose = errors.New("invalid otp purose")
)

// Classifire errors
var (
	ErrClassifierDifficultyRequired = errors.New("classifier: difficulty is required")
	ErrClassifierDifficultyInvalid  = errors.New("classifier: invalid difficulty value")
	ErrClassifierAphasiaRequired    = errors.New("classifier: aphasia type is required")
	ErrClassifierAphasiaInvalid     = errors.New("classifier: invalid aphasia type")
)

// Metadata entity errrors
var (
	ErrMetadataUpdatedBeforeCreated = errors.New("metadata updated before created")
	ErrMetadataUpdatedAtNegative    = errors.New("metadata updated negativly")
	ErrMetadataCreatedAtNegative    = errors.New("metadata created negativly")
)

// pasword entity errors
var (
	ErrPasswordHashInvalidFormat = errors.New("pasword hash invalid")
	ErrPasswordHashInvalidLength = errors.New("pasword hash invalid length")
	ErrPasswordHashRequired      = errors.New("pasword hash req")
)

// OTP validation errors
var (
	ErrOTPValNotCorrectLen = errors.New("otp len not correct")
	ErrOTPValNotNumeric    = errors.New("opt contain only nums")
)

// Session validation errors
var (
	ErrSessionIDRequired = errors.New("session ID is required")
	ErrSessionIDTooLong  = errors.New("session ID is too long")
	ErrSessionIDInvalid  = errors.New("invalid session ID")

	ErrJTIRequired = errors.New("JTI is required")
	ErrJTITooLong  = errors.New("JTI is too long")
	ErrJTIInvalid  = errors.New("invalid JTI")

	ErrFingerprintRequired = errors.New("fingerprint is required")
	ErrFingerprintTooLong  = errors.New("fingerprint is too long")

	ErrLastActivityNegative = errors.New("lastActivity cannot be negative")
	ErrLastActivityFuture   = errors.New("lastActivity too far in the future")

	ErrAccountIDRequired = errors.New("account ID is required")
	ErrAccountIDInvalid  = errors.New("invalid account ID")
)
