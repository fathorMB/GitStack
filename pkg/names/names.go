// Package names validates repository names (rule R11) and defines the
// owner names (users and organizations) reserved because they would clash
// with top-level web paths.
package names

import (
	"errors"
	"slices"
	"strings"
)

// MaxRepoNameLen is the maximum length of a repository name, in bytes.
const MaxRepoNameLen = 100

// Errors returned by ValidateRepoName, one for each R11 rule.
var (
	ErrEmpty       = errors.New("repository name must not be empty")
	ErrTooLong     = errors.New("repository name must be at most 100 characters long")
	ErrInvalidChar = errors.New("repository name may only contain letters, digits, '-', '_' and '.'")
	ErrLeadingDot  = errors.New("repository name must not start with '.'")
	ErrGitSuffix   = errors.New("repository name must not end with '.git'")
)

// ValidateRepoName checks name against rule R11. Uppercase letters are
// allowed and kept as written: uniqueness per owner ignores case, which the
// database enforces. It returns nil or one of the Err* sentinels.
func ValidateRepoName(name string) error {
	if name == "" {
		return ErrEmpty
	}
	if len(name) > MaxRepoNameLen {
		return ErrTooLong
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.'
		if !ok {
			return ErrInvalidChar
		}
	}
	if name[0] == '.' {
		return ErrLeadingDot
	}
	if strings.HasSuffix(strings.ToLower(name), ".git") {
		return ErrGitSuffix
	}
	return nil
}

// reservedOwnerNames are top-level web paths that no user or organization
// may take. Keep sorted.
var reservedOwnerNames = []string{
	"_components",
	"admin",
	"api",
	"assets",
	"change-password",
	"explore",
	"healthz",
	"login",
	"logout",
	"new",
	"notifications",
	"orgs",
	"readyz",
	"repos",
	"settings",
	"static",
	"user",
	"users",
	"v1",
}

// IsReservedOwnerName reports whether name is reserved for users and
// organizations. The comparison is case-insensitive.
func IsReservedOwnerName(name string) bool {
	_, found := slices.BinarySearch(reservedOwnerNames, strings.ToLower(name))
	return found
}

// ReservedOwnerNames returns a sorted copy of the reserved owner names.
func ReservedOwnerNames() []string {
	return slices.Clone(reservedOwnerNames)
}
