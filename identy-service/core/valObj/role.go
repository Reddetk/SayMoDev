package valobj

import corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"

type Role string

const (
	RolePatient       Role = "patient"
	RoleRelative      Role = "relative"
	RoleAdministrator Role = "administrator"
)

// ParseRole validates and converts string to Role
func ParseRole(value string) (Role, error) {
	r := Role(value)
	switch r {
	case RolePatient, RoleRelative, RoleAdministrator:
		return r, nil
	default:
		return "", corerr.ErrInvalidRole
	}
}

func (r Role) String() string { return string(r) }
func (r Role) IsAdmin() bool  { return r == RoleAdministrator }
