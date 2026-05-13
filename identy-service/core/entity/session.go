package entity

import (
	"IAM/core/consts"
	corerr "IAM/core/coreErrors"
	valobj "IAM/core/valObj"
	"time"

	"github.com/google/uuid"
)

// Session -- entity within Account aggregate
// accountID is NOT stored here; belongs to the aggregate root
type Session struct {
	sessionID    string
	jti          string
	fingerprint  string
	lastActivity int64
	metadata     valobj.Metadata
}

// validateSessionFields -- без accountID, он валидируется на уровне Account
func validateSessionFields(sessionID, jti, fingerprint string) error {
	if sessionID == "" {
		return corerr.ErrSessionIDRequired
	}
	if len(sessionID) > consts.MaxSessionIDLen {
		return corerr.ErrSessionIDTooLong
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return corerr.ErrSessionIDInvalid
	}

	if jti == "" {
		return corerr.ErrJTIRequired
	}
	if len(jti) > consts.MaxJTILen {
		return corerr.ErrJTITooLong
	}
	if _, err := uuid.Parse(jti); err != nil {
		return corerr.ErrJTIInvalid
	}

	if fingerprint == "" {
		return corerr.ErrFingerprintRequired
	}
	if len(fingerprint) > consts.MaxFingerprintLen {
		return corerr.ErrFingerprintTooLong
	}
	return nil
}

// newSession -- пакетно-приватный, вызывается только через Account.OpenSession
func newSession(sessionID, jti, fingerprint string) (*Session, error) {
	if err := validateSessionFields(sessionID, jti, fingerprint); err != nil {
		return nil, err
	}
	return &Session{
		sessionID:    sessionID,
		jti:          jti,
		fingerprint:  fingerprint,
		lastActivity: time.Now().UnixMilli(),
		metadata:     valobj.NewMetadataNow(),
	}, nil
}

// RestoreSession -- публичный, только для repository mapper
// accountID принимается здесь как параметр маппинга, НЕ хранится в сущности
func RestoreSession(sessionID, jti, fingerprint string, lastActivity int64, metadata valobj.Metadata) (*Session, error) {
	if err := validateSessionFields(sessionID, jti, fingerprint); err != nil {
		return nil, err
	}
	if lastActivity < 0 {
		return nil, corerr.ErrLastActivityNegative
	}
	return &Session{
		sessionID:    sessionID,
		jti:          jti,
		fingerprint:  fingerprint,
		lastActivity: lastActivity,
		metadata:     metadata,
	}, nil
}

func (s *Session) UpdateActivity() {
	s.lastActivity = time.Now().UnixMilli()
	s.metadata = s.metadata.Touch()
}

func (s *Session) SessionID() string         { return s.sessionID }
func (s *Session) JTI() string               { return s.jti }
func (s *Session) Fingerprint() string       { return s.fingerprint }
func (s *Session) LastActivity() int64       { return s.lastActivity }
func (s *Session) Metadata() valobj.Metadata { return s.metadata }
