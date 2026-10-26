package v1

import (
	"strings"
	"testing"
)

func validEntityTagging(accountID int) *EntityTagging {
	return &EntityTagging{
		Spec: EntityTaggingSpec{
			TagBaseSpec: TagBaseSpec{
				APIKey:      "key",
				AccountID:   accountID,
				Region:      "US",
				EntityNames: []string{"Entity1"},
				Tags:        []*EntityTag{{Key: "tag1", Values: []string{"value1"}}},
			},
		},
	}
}

func TestEntityTaggingApplyDefaultsIgnoresDefaultAccountID(t *testing.T) {
	// the operator default account (when configured) must never be applied to EntityTagging
	et := validEntityTagging(0)
	et.ApplyDefaults()

	if et.Spec.AccountID != 0 {
		t.Errorf("expected accountId to remain 0, got %d", et.Spec.AccountID)
	}
}

func TestEntityTaggingValidateUpdateRejectsAccountIDChange(t *testing.T) {
	cases := map[string]struct {
		oldID, newID int
		wantErr      bool
	}{
		"unset to set": {oldID: 0, newID: 123, wantErr: true},
		"changed":      {oldID: 123, newID: 456, wantErr: true},
		"set to unset": {oldID: 123, newID: 0, wantErr: true},
		"unchanged":    {oldID: 123, newID: 123, wantErr: false},
		"both unset":   {oldID: 0, newID: 0, wantErr: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validEntityTagging(tc.newID).ValidateUpdate(validEntityTagging(tc.oldID))
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "accountId is immutable") {
					t.Errorf("expected immutable accountId error, got %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestEntityTaggingValidateRejectsNegativeAccountID(t *testing.T) {
	_, err := validEntityTagging(-1).ValidateCreate()
	if err == nil || !strings.Contains(err.Error(), "accountId cannot be negative") {
		t.Errorf("expected negative accountId error, got %v", err)
	}

	if _, err := validEntityTagging(1).ValidateCreate(); err != nil {
		t.Errorf("unexpected error for valid accountId: %v", err)
	}
}

func TestEntityTaggingRejectsUnsafeEntityNames(t *testing.T) {
	cases := map[string]string{
		"single quote": "bad'name",
		"backslash":    `bad\name`,
	}

	for name, entityName := range cases {
		t.Run(name, func(t *testing.T) {
			create := validEntityTagging(0)
			create.Spec.EntityNames = []string{"Entity1", entityName}
			if _, err := create.ValidateCreate(); err == nil || !strings.Contains(err.Error(), "single quotes or backslashes") {
				t.Errorf("create: expected unsafe entity name error, got %v", err)
			}

			update := validEntityTagging(0)
			update.Spec.EntityNames = []string{entityName}
			if _, err := update.ValidateUpdate(validEntityTagging(0)); err == nil || !strings.Contains(err.Error(), "single quotes or backslashes") {
				t.Errorf("update: expected unsafe entity name error, got %v", err)
			}
		})
	}
}
