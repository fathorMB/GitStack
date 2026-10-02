package main

import (
	"strings"
	"testing"
)

// La dichiarazione del permesso è obbligatoria sulle rotte di core con
// {resourceId} e ammessa solo lì.
func TestBuild_Permesso(t *testing.T) {
	auth := []map[string][]string{{"bearerAuth": nil}}
	cases := []struct {
		name    string
		path    string
		op      operation
		wantErr string
		wantPer string
	}{
		{"core con permesso", "/resources/{resourceId}", operation{OperationID: "a", Tags: []string{"resources"}, RequiredPermission: "admin"}, "", "PermissionAdmin"},
		{"core senza permesso", "/resources/{resourceId}", operation{OperationID: "a", Tags: []string{"resources"}}, "senza x-required-permission", ""},
		{"core senza {resourceId}", "/resources", operation{OperationID: "a", Tags: []string{"resources"}}, "", "PermissionNone"},
		{"valore non valido", "/resources/{resourceId}", operation{OperationID: "a", Tags: []string{"resources"}, RequiredPermission: "owner"}, "non valido", ""},
		{"permesso senza {resourceId}", "/resources", operation{OperationID: "a", Tags: []string{"resources"}, RequiredPermission: "read"}, "senza {resourceId}", ""},
		{"permesso su identity", "/resources/{resourceId}/grants", operation{OperationID: "a", Tags: []string{"permissions"}, RequiredPermission: "admin"}, "solo alle rotte di core", ""},
		{"identity su {resourceId} senza permesso", "/resources/{resourceId}/grants", operation{OperationID: "a", Tags: []string{"permissions"}}, "", "PermissionNone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _, err := build(document{Security: auth}, "GET", c.path, c.op)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("errore = %v, voluto %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := permission(r.Permission); got != c.wantPer {
				t.Errorf("permesso = %s, voluto %s", got, c.wantPer)
			}
		})
	}
}

func TestBuild_RottaPubblicaConPermesso(t *testing.T) {
	op := operation{OperationID: "a", Tags: []string{"resources"}, RequiredPermission: "read", Security: &[]map[string][]string{}}
	if _, _, err := build(document{}, "GET", "/resources/{resourceId}", op); err == nil {
		t.Fatal("rotta pubblica con x-required-permission accettata")
	}
}
