// Package domain holds the application's types and pure computation. It has no
// provider, network, execution or database dependency.
package domain

import (
	"regexp"
)

const SchemaVersion = "instance-2.0"

type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string           { return e.Code }
func Fail(code string, status int) error { return &Error{code, status} }

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func ValidID(v string) bool { return idPattern.MatchString(v) }
func Has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type Binding struct {
	InstanceID  string `json:"instance_id"`
	Environment string `json:"environment"`
}

func (b Binding) Valid() bool {
	return ValidID(b.InstanceID) && Has([]string{"SIM", "LIVE"}, b.Environment)
}

type ReadPrincipal struct {
	PrincipalID          string  `json:"principal_id"`
	Binding              Binding `json:"binding"`
	Read                 bool    `json:"read"`
	AuthorizationVersion string  `json:"authorization_version"`
}

func (p ReadPrincipal) Valid() bool {
	if !p.Binding.Valid() || !p.Read || !ValidID(p.PrincipalID) || !ValidID(p.AuthorizationVersion) {
		return false
	}
	return true
}
