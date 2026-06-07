// Package entity contains domain entities and aggregate roots for BC#1
// Identity and Access Management.
//
// Entities have identity and mutable state. State changes happen exclusively
// through domain methods that enforce invariants -- never by direct field access.
//
// Aggregates:
//   - Account -- aggregate root, owns Sessions and PasswordHistory
//
// Entities:
//   - Session -- authenticated session within Account aggregate
//
// Constructors follow two patterns:
//   - NewXxx    -- creates new domain object, used in application services
//   - RestoreXxx -- rebuilds object from persistence, used by repository mappers
package entity

import (
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/core/consts"
	"github.com/Reddetk/SayMoDev/identy-service/port/in"

	"github.com/google/uuid"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// Account represents the Account aggregate root
type Account struct {
	uuid            string
	email           string
	personalInfo    string
	role            valobj.Role
	googleUID       *string
	passwordHash    *string
	revision        int64
	status          valobj.AccountStatus
	lockedUntil     *int64
	metadata        valobj.Metadata
	sessions        []Session
	passwordHistory []valobj.PasswordEntry
}

func validateAccountArgs(email, personalInfo string, role valobj.Role, googleUID, passwordHash *string) error {
	if email == "" {
		return corerr.ErrEmailRequired
	}
	if len(email) > consts.MaxEmailLength {
		return corerr.ErrEmailTooLong
	}
	if !consts.EmailRegex.MatchString(email) {
		return corerr.ErrInvalidEmail
	}

	if personalInfo == "" {
		return corerr.ErrPersonalInfoRequired
	}
	if len(personalInfo) > consts.MaxPersonalInfoLen {
		return corerr.ErrPersonalInfoTooLong
	}

	if role == "" {
		return corerr.ErrRoleRequired
	}

	hasGoogle := googleUID != nil && *googleUID != ""
	hasPassword := passwordHash != nil && *passwordHash != ""

	if hasGoogle && hasPassword {
		return corerr.ErrAccountCannotHaveBoth
	}
	if !hasGoogle && !hasPassword {
		return corerr.ErrAccountRequiresAuth
	}

	if hasGoogle {
		if len(*googleUID) > consts.MaxGoogleUIDLen {
			return corerr.ErrGoogleUIDTooLong
		}
	}

	if hasPassword {
		if len(*passwordHash) != 60 {
			return corerr.ErrInvalidPasswordHashLength
		}
		if !consts.BcryptHashRegex.MatchString(*passwordHash) {
			return corerr.ErrPasswordHashInvalidFormat
		}
	}

	return nil
}

// newAccount -- приватный базовый конструктор, общая инициализация новых аккаунтов
func newAccount(
	email, personalInfo string,
	role valobj.Role,
	googleUID, passwordHash *string,
) (*Account, error) {
	if err := validateAccountArgs(email, personalInfo, role, googleUID, passwordHash); err != nil {
		return nil, err
	}
	return &Account{
		uuid:         uuid.NewString(),
		email:        email,
		personalInfo: personalInfo,
		role:         role,
		googleUID:    googleUID,
		passwordHash: passwordHash,
		revision:     1,
		status:       valobj.StatusActive,
		metadata:     valobj.NewMetadataNow(),
	}, nil
}

// NotSafeGhange you can not change AccountID, email by this, but can personalInfo,
// JUST FOR DEBUGGING -|role,status, metadata|
func (a *Account) NotSafeGhange(params in.AccountDTO) (*Account, error) {
	a.personalInfo = params.PersonalInfo
	var err error
	a.role, err = valobj.ParseRole(params.Role)
	if err != nil {
		return nil, err
	}
	a.status, err = valobj.ParseAccountStatus(params.Status)
	if err != nil {
		return nil, err
	}

	a.metadata = a.Metadata().Touch()
	return a, nil
}

func NewAccount(email, personalInfo string, role valobj.Role, passwordHash string) (*Account, error) {
	return newAccount(email, personalInfo, role, nil, &passwordHash)
}

func NewOAuthAccount(email, personalInfo string, role valobj.Role, googleUID string) (*Account, error) {
	return newAccount(email, personalInfo, role, &googleUID, nil)
}

// RestoreAccount -- отдельный, не через newAccount: принимает полное состояние из БД
// rebuilds Account from persistence -- used by repository mapper
func RestoreAccount(
	id, email, personalInfo string,
	role valobj.Role,
	status valobj.AccountStatus,
	googleUID, passwordHash *string,
	revision int64,
	lockedUntil *int64,
	metadata valobj.Metadata,
	sessions []Session,
	passwordHistory []valobj.PasswordEntry,
) (*Account, error) {
	if err := validateAccountArgs(email, personalInfo, role, googleUID, passwordHash); err != nil {
		return nil, err
	}
	return &Account{
		uuid:            id,
		email:           email,
		personalInfo:    personalInfo,
		role:            role,
		googleUID:       googleUID,
		passwordHash:    passwordHash,
		revision:        revision,
		status:          status,
		lockedUntil:     lockedUntil,
		metadata:        metadata,
		sessions:        sessions,
		passwordHistory: passwordHistory,
	}, nil
}

// -- Domain methods --

// OpenSession -- единственная точка создания сессии (G5: eviction, max 5)
// Возвращает evictedJTI для немедленного занесения в blacklist (G9)
func (a *Account) OpenSession(sessionID, jti, fingerprint string) (session *Session, evictedJTI string, err error) {
	if a.status != valobj.StatusActive {
		return nil, "", corerr.ErrAccountNotActive
	}
	if len(a.sessions) >= consts.MaxSessionsPerAccount {
		evictedJTI = a.sessions[a.oldestSessionIndex()].jti
		a.sessions = append(a.sessions[:a.oldestSessionIndex()], a.sessions[a.oldestSessionIndex()+1:]...)
	}
	s, err := newSession(sessionID, jti, fingerprint)
	if err != nil {
		return nil, "", err
	}
	a.sessions = append(a.sessions, *s)
	a.metadata = a.metadata.Touch()
	return s, evictedJTI, nil
}

func (a *Account) MapToDTO() *in.AccountDTO {
	return &in.AccountDTO{
		ID:           a.uuid,
		Email:        a.email,
		PersonalInfo: a.PersonalInfo(),
		Role:         a.Role().String(),
		Status:       a.Status().String(),
		LockedUntil:  a.lockedUntil,
		Metadata:     a.Metadata().String(),
		PasswordHash: *a.PasswordHash(),
		Rev:          a.revision,
	}
}

// RevokeSession -- удалить конкретную сессию (E4 logout, E14 admin terminate)
// Возвращает jti для blacklist
func (a *Account) RevokeSession(sessionID string) (jti string, err error) {
	for i, s := range a.sessions {
		if s.sessionID == sessionID {
			jti = s.jti
			a.sessions = append(a.sessions[:i], a.sessions[i+1:]...)
			a.metadata = a.metadata.Touch()
			return jti, nil
		}
	}
	return "", corerr.ErrSessionNotFound
}

// RevokeAllSessions -- сброс всех сессий, возвращает все jti для blacklist (T4)
func (a *Account) RevokeAllSessions() []string {
	jtis := make([]string, len(a.sessions))
	for i, s := range a.sessions {
		jtis[i] = s.jti
	}
	a.sessions = nil
	return jtis
}

// TouchSession -- обновить lastActivity конкретной сессии
func (a *Account) TouchSession(sessionID string) error {
	for i := range a.sessions {
		if a.sessions[i].sessionID == sessionID {
			a.sessions[i].UpdateActivity()
			return nil
		}
	}
	return corerr.ErrSessionNotFound
}

// FindSessionByJTI -- для валидации токена (G7)
func (a *Account) FindSessionByJTI(jti string) (*Session, error) {
	for i := range a.sessions {
		if a.sessions[i].jti == jti {
			return &a.sessions[i], nil
		}
	}
	return nil, corerr.ErrSessionNotFound
}

func (a *Account) oldestSessionIndex() int {
	oldest := 0
	for i, s := range a.sessions {
		if s.lastActivity < a.sessions[oldest].lastActivity {
			oldest = i
		}
	}
	return oldest
}

// Lock performs T4 Mass-Revoke and transitions status to StatusBlocked.
//
// Invariants:
//   - ErrAccountDeleted  -- cannot lock a deleted account
//   - ErrAccountAlreadyLocked -- idempotency guard; prevents silent rev++ on double-lock
//
// Returns revokedJTIs for blacklist propagation (§6).
func (a *Account) Lock(until *int64) (revokedJTIs []string, err error) {
	if a.status == valobj.StatusDeleted {
		return nil, corerr.ErrAccountDeleted
	}
	if a.status == valobj.StatusBlocked {
		return nil, corerr.ErrAccountAlreadyLocked
	}
	revokedJTIs = a.RevokeAllSessions()
	a.status = valobj.StatusBlocked
	a.lockedUntil = until
	a.revision++
	a.metadata = a.metadata.Touch()
	return revokedJTIs, nil
}

// Unlock restores status to StatusActive and clears lockedUntil.
//
// Invariants:
//   - ErrAccountDeleted  -- cannot unlock a deleted account
//   - ErrAccountNotLocked -- idempotency guard; prevents silent status overwrite on active account
//
// Does not perform T4 Mass-Revoke -- account has no sessions while blocked.
func (a *Account) Unlock() error {
	if a.status == valobj.StatusDeleted {
		return corerr.ErrAccountDeleted
	}
	if a.status != valobj.StatusBlocked {
		return corerr.ErrAccountNotLocked
	}
	a.status = valobj.StatusActive
	a.lockedUntil = nil
	a.metadata = a.metadata.Touch()
	return nil
}

// ChangePassword updates hash and increments revision -- T4 Mass-Revoke
func (a *Account) ChangePassword(newHash string) (revokedJTIs []string, err error) {
	if len(newHash) != 60 {
		return nil, corerr.ErrInvalidPasswordHashLength
	}
	if !consts.BcryptHashRegex.MatchString(newHash) {
		return nil, corerr.ErrPasswordHashInvalidFormat
	}
	revokedJTIs = a.RevokeAllSessions()
	a.passwordHash = &newHash
	a.revision++
	a.metadata = a.metadata.Touch()
	return revokedJTIs, nil
}

func (a *Account) SoftDelete() (revokedJTIs []string, err error) {
	if a.status == valobj.StatusDeleted {
		return nil, corerr.ErrAccountAlreadyDeleted
	}
	revokedJTIs = a.RevokeAllSessions()
	a.status = valobj.StatusDeleted
	a.revision++
	a.metadata = a.metadata.Touch()
	return revokedJTIs, nil
}

// AddPasswordHistory appends entry, trims to max 5
func (a *Account) AddPasswordHistory(entry valobj.PasswordEntry) {
	a.passwordHistory = append(a.passwordHistory, entry)
	if len(a.passwordHistory) > consts.MaxPasswordHistory {
		a.passwordHistory = a.passwordHistory[len(a.passwordHistory)-consts.MaxPasswordHistory:]
	}
}

// IsLocked checks if account is currently locked
func (a *Account) IsLocked() bool {
	if a.status != valobj.StatusBlocked {
		return false
	}
	if a.lockedUntil == nil {
		return true
	}
	return time.Now().UnixMilli() < *a.lockedUntil
}

// -- Getters --

func (a *Account) UUID() string                 { return a.uuid }
func (a *Account) Email() string                { return a.email }
func (a *Account) PersonalInfo() string         { return a.personalInfo }
func (a *Account) Role() valobj.Role            { return a.role }
func (a *Account) GoogleUID() *string           { return a.googleUID }
func (a *Account) PasswordHash() *string        { return a.passwordHash }
func (a *Account) Revision() int64              { return a.revision }
func (a *Account) Status() valobj.AccountStatus { return a.status }
func (a *Account) LockedUntil() *int64          { return a.lockedUntil }
func (a *Account) Metadata() valobj.Metadata    { return a.metadata }
func (a *Account) Sessions() []Session          { return a.sessions }
func (a *Account) SessionsDTO() []in.SessionDTO {
	SessionsDTOs := []in.SessionDTO{}
	for _, session := range a.Sessions() {
		SessionsDTOs = append(SessionsDTOs, *session.MapToDTO())
	}
	return SessionsDTOs
}
func (a *Account) PasswordHistory() []valobj.PasswordEntry { return a.passwordHistory }
