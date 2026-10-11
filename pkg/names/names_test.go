package names

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestValidateRepoName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"single char", "a", nil},
		{"100 chars", strings.Repeat("a", 100), nil},
		{"101 chars", strings.Repeat("a", 101), ErrTooLong},
		{"empty", "", ErrEmpty},
		{"mixed valid", "my-repo_1.0", nil},
		{"digits only", "123", nil},
		{"git suffix lookalike", "x.gitx", nil},
		{"dot inside", "a.b", nil},
		{"uppercase", "Repo", nil},
		{"uppercase inside", "rePo", nil},
		{"camel case", "GitStack", nil},
		{"all caps", "GITSTACK", nil},
		{"leading uppercase digit", "A1", nil},
		{"git suffix uppercase", "x.GIT", ErrGitSuffix},
		{"git suffix mixed", "x.Git", ErrGitSuffix},
		{"non ascii", "rèpo", ErrInvalidChar},
		{"space", "a b", ErrInvalidChar},
		{"slash", "a/b", ErrInvalidChar},
		{"leading dot", ".hidden", ErrLeadingDot},
		{"single dot", ".", ErrLeadingDot},
		{"git suffix", "x.git", ErrGitSuffix},
		{"only .git", ".git", ErrLeadingDot},
		{"101 chars with uppercase", strings.Repeat("A", 101), ErrTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoName(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateRepoName(%q) = %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

func TestIsReservedOwnerName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"login", true},
		{"Login", true},
		{"ADMIN", true},
		{"alice", false},
		{"", false},
		{"logins", false},
	}
	for _, tt := range tests {
		if got := IsReservedOwnerName(tt.in); got != tt.want {
			t.Errorf("IsReservedOwnerName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestReservedOwnerNamesList(t *testing.T) {
	list := ReservedOwnerNames()
	if !slices.IsSorted(list) {
		t.Errorf("list not sorted: %v", list)
	}
	required := []string{
		"login", "logout", "settings", "api", "admin", "new", "explore",
		"orgs", "users", "user", "repos", "healthz", "readyz", "assets",
		"static", "change-password", "notifications", "_components", "v1",
	}
	for _, n := range required {
		if !IsReservedOwnerName(n) {
			t.Errorf("%q should be reserved", n)
		}
		if !slices.Contains(list, n) {
			t.Errorf("%q missing from list", n)
		}
	}
	list[0] = "mutated"
	if ReservedOwnerNames()[0] == "mutated" {
		t.Error("ReservedOwnerNames must return a copy")
	}
}
