package controller

import (
	"fmt"
	"strings"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestBuildEntitySearchQuery(t *testing.T) {
	cases := map[string]struct {
		names     []string
		accountID int
		want      string
	}{
		"no account": {
			names: []string{"Entity1"},
			want:  "reporting is true and name in ('Entity1')",
		},
		"with account": {
			names:     []string{"Entity1"},
			accountID: 12345,
			want:      "reporting is true and name in ('Entity1') and accountId = 12345",
		},
		"multiple names with account": {
			names:     []string{"Entity1", "Entity2"},
			accountID: 42,
			want:      "reporting is true and name in ('Entity1','Entity2') and accountId = 42",
		},
		"multiple names no account": {
			names: []string{"Entity1", "Entity2"},
			want:  "reporting is true and name in ('Entity1','Entity2')",
		},
		"negative account is ignored": {
			names:     []string{"Entity1"},
			accountID: -1,
			want:      "reporting is true and name in ('Entity1')",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := buildEntitySearchQuery(tc.names, tc.accountID); got != tc.want {
				t.Errorf("buildEntitySearchQuery() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateEntityNames(t *testing.T) {
	cases := map[string]struct {
		names   []string
		wantErr string
	}{
		"clean names":        {names: []string{"Entity1", "my-service (prod)"}},
		"empty list":         {names: nil},
		"single quote":       {names: []string{"Entity1", "bad'name"}, wantErr: "bad'name"},
		"injection attempt":  {names: []string{"x') or name like ('"}, wantErr: "x') or name like ('"},
		"backslash":          {names: []string{`bad\name`}, wantErr: `bad\name`},
		"trailing backslash": {names: []string{`name\`}, wantErr: `name\`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateEntityNames(tc.names)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.wantErr)) {
				t.Errorf("expected error naming %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestFetchEntitiesByNameRejectsUnsafeNamesBeforeQuery(t *testing.T) {
	// no Entities client is set, so reaching the search would panic
	r := &EntityTaggingReconciler{Log: ctrl.Log.WithName("test")}

	if _, err := r.fetchEntitiesByName([]string{"bad'name"}, 0); err == nil {
		t.Fatal("expected an error for a name containing a single quote")
	}
}
