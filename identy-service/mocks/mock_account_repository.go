// Package mocks contains testify/mock implementations of all out-port interfaces.
// Each mock covers the happy path and the primary error path documented
// in port/out/*.go comments.
//
// Usage:
//
//	repo := new(mocks.MockAccountRepository)
//	repo.On("FindByEmail", mock.Anything, "user@example.com").
//	    Return(testdata.NewActiveAccount(), nil)
//	// ... inject repo into use-case under test ...
//	repo.AssertExpectations(t)
package mocks

import (
	"context"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/internal/core/entity"
	"github.com/stretchr/testify/mock"
)

// MockAccountRepository is a testify mock for out.AccountRepository.
// It intentionally does not embed a default implementation  every
// method call must be explicitly configured with On(...).Return(...)
// so missing expectations cause test failures, not silent panics.
type MockAccountRepository struct {
	mock.Mock
}

// CreateAccountWithTx saves a new account aggregate in an ACID transaction.
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrEmailAlreadyExists)
func (m *MockAccountRepository) CreateAccountWithTx(
	ctx context.Context,
	account *entity.Account,
) (string, error) {
	args := m.Called(ctx, account)
	return args.String(0), args.Error(1)
}

// ResetPassword atomically updates password hash, increments revision,
// clears all sessions, and writes jti entries to the outbox blacklist.
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrAccountNotFound)
func (m *MockAccountRepository) ResetPassword(
	ctx context.Context,
	account *entity.Account,
	newPasswordHash string,
) error {
	args := m.Called(ctx, account, newPasswordHash)
	return args.Error(0)
}

// UpdateAccountStatusTx atomically updates status, lockedUntil, revision,
// clears sessions, and writes revoked JTIs to the outbox blacklist.
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrAccountNotFound)
func (m *MockAccountRepository) UpdateAccountStatusTx(
	ctx context.Context,
	account *entity.Account,
	revokedJTIs []string,
	actorID string,
) error {
	args := m.Called(ctx, account, revokedJTIs, actorID)
	return args.Error(0)
}

// EmailExist checks whether an email is already registered without loading
// the full aggregate. Used in registration flow for anti-enumeration.
//
// Happy path (free email):  Return(false, nil)
// Happy path (taken email): Return(true,  nil)
// Error path:               Return(false, corerr.ErrAccountRepository)
func (m *MockAccountRepository) EmailExist(
	ctx context.Context,
	email string,
) (bool, error) {
	args := m.Called(ctx, email)
	return args.Bool(0), args.Error(1)
}

// FindByEmail loads the full Account aggregate by email address.
// For unauthenticated flows only: Login, ConfirmPasswordReset, Register.
//
// Happy path:  Return(account, nil)
// Error path:  Return(nil, corerr.ErrAccountNotFound)
func (m *MockAccountRepository) FindByEmail(
	ctx context.Context,
	email string,
) (*entity.Account, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entity.Account), args.Error(1)
}

// FindByAccountID loads the full Account aggregate (with sessions) by UUID.
// Used in all authenticated flows where accountID comes from AuthContext.
//
// Happy path:  Return(account, nil)
// Error path:  Return(nil, corerr.ErrAccountNotFound)
func (m *MockAccountRepository) FindByAccountID(
	ctx context.Context,
	accountID string,
) (*entity.Account, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entity.Account), args.Error(1)
}

// SaveSessionWithTx persists the Account state after account.OpenSession().
// ACID transaction: upsert sessions + optional evicted JTI in blacklist.
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrAccountRepository)
func (m *MockAccountRepository) SaveSessionWithTx(
	ctx context.Context,
	account *entity.Account,
	evictedJTI string,
) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

// DeleteSessionWithTx removes a session and writes its JTI to the blacklist
// in a single ACID transaction. Called after account.RevokeSession().
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrAccountNotFound)
func (m *MockAccountRepository) DeleteSessionWithTx(
	ctx context.Context,
	account *entity.Account,
	revokedJTI string,
) error {
	args := m.Called(ctx, account, revokedJTI)
	return args.Error(0)
}

// FindByGoogleUID loads the Account aggregate by google_uid (Google claim sub).
// Priority lookup for OAuth flow  stable across Google email changes.
//
// Happy path:  Return(account, nil)
// Error path:  Return(nil, corerr.ErrAccountNotFound)
func (m *MockAccountRepository) FindByGoogleUID(
	ctx context.Context,
	googleUID string,
) (*entity.Account, error) {
	args := m.Called(ctx, googleUID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entity.Account), args.Error(1)
}

// LinkGoogleUID binds a google_uid to an existing email/password account.
// Atomic UPDATE ... WHERE google_uid IS NULL prevents double-binding.
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrAccountNotFound)
func (m *MockAccountRepository) LinkGoogleUID(
	ctx context.Context,
	accountID string,
	googleUID string,
) error {
	args := m.Called(ctx, accountID, googleUID)
	return args.Error(0)
}

// CreateOAuthAccountWithTx creates an OAuth account in an ACID transaction:
// INSERT account (password_hash=NULL) + outbox AccountRegistered event.
//
// Happy path:  Return(account, nil)
// Error path:  Return(nil, corerr.ErrEmailAlreadyExists)
func (m *MockAccountRepository) CreateOAuthAccountWithTx(
	ctx context.Context,
	params *entity.Account,
) (*entity.Account, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entity.Account), args.Error(1)
}

// ChangeAccountData performs an unsafe admin update of general account data
// (personalInfo, role, status). Does not touch passwordHash or sessions.
//
// Happy path:  Return(nil)
// Error path:  Return(corerr.ErrAccountNotFound)
func (m *MockAccountRepository) ChangeAccountData(
	ctx context.Context,
	params *entity.Account,
) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
// Fails at build time if MockAccountRepository drifts from out.AccountRepository.
var _ interface {
	CreateAccountWithTx(context.Context, *entity.Account) (string, error)
	ResetPassword(context.Context, *entity.Account, string) error
	UpdateAccountStatusTx(context.Context, *entity.Account, []string, string) error
	EmailExist(context.Context, string) (bool, error)
	FindByEmail(context.Context, string) (*entity.Account, error)
	FindByAccountID(context.Context, string) (*entity.Account, error)
	SaveSessionWithTx(context.Context, *entity.Account, string) error
	DeleteSessionWithTx(context.Context, *entity.Account, string) error
	FindByGoogleUID(context.Context, string) (*entity.Account, error)
	LinkGoogleUID(context.Context, string, string) error
	CreateOAuthAccountWithTx(context.Context, *entity.Account) (*entity.Account, error)
	ChangeAccountData(context.Context, *entity.Account) error
} = (*MockAccountRepository)(nil)

// Sentinel errors re-exported for use in test On(...).Return(...) calls
// without importing corerr directly in every _test.go file.
var (
	ErrAccountNotFound    = corerr.ErrAccountNotFound
	ErrEmailAlreadyExists = corerr.ErrEmailAlreadyExists
	ErrAccountRepository  = corerr.ErrAccountRepository
)
