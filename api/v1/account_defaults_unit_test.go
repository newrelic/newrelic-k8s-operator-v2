package v1

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// accountDefaultKinds covers the six kinds that honor the operator default
// account ID. EntityTagging is deliberately absent.
var accountDefaultKinds = []struct {
	name string
	// apply runs ApplyDefaults on a CR with the given accountId and returns the resulting accountId.
	apply func(accountID int) int
	// validate runs ValidateAPIKeyOrSecret on a CR with the given accountId and an inline apiKey.
	validate func(accountID int) []string
}{
	{
		name: "AlertPolicy",
		apply: func(id int) int {
			r := &AlertPolicy{Spec: AlertPolicySpec{AccountID: id}}
			r.ApplyDefaults()
			return r.Spec.AccountID
		},
		validate: func(id int) []string {
			r := &AlertPolicy{Spec: AlertPolicySpec{AccountID: id, APIKey: "key"}}
			return validationDetails(r.ValidateAPIKeyOrSecret())
		},
	},
	{
		name: "NrqlCondition",
		apply: func(id int) int {
			r := &NrqlCondition{}
			r.Spec.AccountID = id
			r.ApplyDefaults()
			return r.Spec.AccountID
		},
		validate: func(id int) []string {
			r := &NrqlCondition{}
			r.Spec.AccountID = id
			r.Spec.APIKey = "key"
			return validationDetails(r.ValidateAPIKeyOrSecret())
		},
	},
	{
		name: "AlertChannel",
		apply: func(id int) int {
			r := &AlertChannel{Spec: AlertChannelSpec{AccountID: id}}
			r.ApplyDefaults()
			return r.Spec.AccountID
		},
		validate: func(id int) []string {
			r := &AlertChannel{Spec: AlertChannelSpec{AccountID: id, APIKey: "key"}}
			return validationDetails(r.ValidateAPIKeyOrSecret())
		},
	},
	{
		name: "AlertDestination",
		apply: func(id int) int {
			r := &AlertDestination{}
			r.Spec.AccountID = id
			r.ApplyDefaults()
			return r.Spec.AccountID
		},
		validate: func(id int) []string {
			r := &AlertDestination{}
			r.Spec.AccountID = id
			r.Spec.APIKey = "key"
			return validationDetails(r.ValidateAPIKeyOrSecret())
		},
	},
	{
		name: "AlertWorkflow",
		apply: func(id int) int {
			r := &AlertWorkflow{Spec: AlertWorkflowSpec{AccountID: id}}
			r.ApplyDefaults()
			return r.Spec.AccountID
		},
		validate: func(id int) []string {
			r := &AlertWorkflow{Spec: AlertWorkflowSpec{AccountID: id, APIKey: "key"}}
			return validationDetails(r.ValidateAPIKeyOrSecret())
		},
	},
	{
		name: "ServiceLevel",
		apply: func(id int) int {
			r := &ServiceLevel{Spec: ServiceLevelSpec{AccountID: id}}
			r.ApplyDefaults()
			return r.Spec.AccountID
		},
		validate: func(id int) []string {
			r := &ServiceLevel{Spec: ServiceLevelSpec{AccountID: id, APIKey: "key"}}
			return validationDetails(r.ValidateAPIKeyOrSecret())
		},
	},
}

// validationDetails flattens a field.ErrorList into its error strings.
func validationDetails(errs field.ErrorList) []string {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return msgs
}

// setDefaultAccountIDForTest sets the process-wide default and resets it afterwards.
// Tests using it must not run in parallel.
func setDefaultAccountIDForTest(t *testing.T, id int) {
	t.Helper()
	SetDefaultAccountID(id)
	t.Cleanup(func() { SetDefaultAccountID(0) })
}

func TestApplyDefaultsSetsAccountIDFromOperatorDefault(t *testing.T) {
	setDefaultAccountIDForTest(t, 1234)

	for _, k := range accountDefaultKinds {
		t.Run(k.name, func(t *testing.T) {
			if got := k.apply(0); got != 1234 {
				t.Errorf("accountId = %d, want 1234", got)
			}
		})
	}
}

func TestApplyDefaultsExplicitAccountIDWins(t *testing.T) {
	setDefaultAccountIDForTest(t, 1234)

	for _, k := range accountDefaultKinds {
		t.Run(k.name, func(t *testing.T) {
			if got := k.apply(999); got != 999 {
				t.Errorf("accountId = %d, want explicit 999 to be kept", got)
			}
		})
	}
}

func TestApplyDefaultsNoDefaultLeavesZero(t *testing.T) {
	setDefaultAccountIDForTest(t, 0)

	for _, k := range accountDefaultKinds {
		t.Run(k.name, func(t *testing.T) {
			if got := k.apply(0); got != 0 {
				t.Errorf("accountId = %d, want 0 when no default is configured", got)
			}
		})
	}
}

func TestSetDefaultAccountIDIgnoresNegative(t *testing.T) {
	setDefaultAccountIDForTest(t, -5)

	if got := DefaultAccountID(); got != 0 {
		t.Errorf("DefaultAccountID() = %d, want 0 for a negative input", got)
	}
}

func TestValidateAPIKeyOrSecretAccountIDRequiredMentionsDefault(t *testing.T) {
	setDefaultAccountIDForTest(t, 0)

	for _, k := range accountDefaultKinds {
		t.Run(k.name, func(t *testing.T) {
			msgs := k.validate(0)
			if len(msgs) != 1 {
				t.Fatalf("got %d errors %v, want exactly the accountId error", len(msgs), msgs)
			}
			for _, want := range []string{"spec.accountId", "--default-account-id", "NEW_RELIC_ACCOUNT_ID", "controllerManager.manager.defaultAccountId"} {
				if !strings.Contains(msgs[0], want) {
					t.Errorf("error %q does not mention %q", msgs[0], want)
				}
			}
		})
	}
}

func TestValidateAPIKeyOrSecretPassesAfterDefaulting(t *testing.T) {
	setDefaultAccountIDForTest(t, 1234)

	for _, k := range accountDefaultKinds {
		t.Run(k.name, func(t *testing.T) {
			// Mirrors admission order: the mutating webhook runs before validation.
			defaulted := k.apply(0)
			if msgs := k.validate(defaulted); len(msgs) != 0 {
				t.Errorf("unexpected validation errors after defaulting: %v", msgs)
			}
		})
	}
}

func TestServiceLevelImmutableAccountIDErrorHintsDefault(t *testing.T) {
	old := &ServiceLevel{Spec: ServiceLevelSpec{AccountID: 111}}

	t.Run("new value from operator default carries hint", func(t *testing.T) {
		setDefaultAccountIDForTest(t, 222)
		updated := &ServiceLevel{Spec: ServiceLevelSpec{AccountID: 222}}

		errs := updated.ValidateImmutableFields(old)
		if len(errs) != 1 {
			t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
		}
		msg := errs[0].Error()
		if !strings.Contains(msg, "operator default") || !strings.Contains(msg, "spec.accountId: 111") {
			t.Errorf("error %q missing the operator-default hint", msg)
		}
	})

	t.Run("explicit different value has no hint", func(t *testing.T) {
		setDefaultAccountIDForTest(t, 222)
		updated := &ServiceLevel{Spec: ServiceLevelSpec{AccountID: 333}}

		errs := updated.ValidateImmutableFields(old)
		if len(errs) != 1 {
			t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
		}
		if strings.Contains(errs[0].Error(), "operator default") {
			t.Errorf("error %q should not carry the operator-default hint", errs[0].Error())
		}
	})

	t.Run("no default configured has no hint", func(t *testing.T) {
		setDefaultAccountIDForTest(t, 0)
		updated := &ServiceLevel{Spec: ServiceLevelSpec{AccountID: 0}}

		errs := updated.ValidateImmutableFields(old)
		if len(errs) != 1 {
			t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
		}
		if strings.Contains(errs[0].Error(), "operator default") {
			t.Errorf("error %q should not carry the operator-default hint", errs[0].Error())
		}
	})
}

func TestAlertPolicyValidateUpdateRejectsAccountIDChange(t *testing.T) {
	newPolicy := func(accountID int) *AlertPolicy {
		return &AlertPolicy{Spec: AlertPolicySpec{
			Name:               "policy",
			IncidentPreference: IncidentPreferencePerCondition,
			Region:             "US",
			AccountID:          accountID,
			APIKey:             "key",
		}}
	}

	t.Run("changed accountId is rejected", func(t *testing.T) {
		_, err := newPolicy(222).ValidateUpdate(newPolicy(111))
		if err == nil {
			t.Fatalf("expected an error when accountId changes")
		}
		if !strings.Contains(err.Error(), "spec.accountId") || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("error %q should name spec.accountId as immutable", err.Error())
		}
		if strings.Contains(err.Error(), "operator default") {
			t.Errorf("error %q should not carry the operator-default hint", err.Error())
		}
	})

	t.Run("unchanged accountId is accepted", func(t *testing.T) {
		if _, err := newPolicy(111).ValidateUpdate(newPolicy(111)); err != nil {
			t.Fatalf("unexpected error for unchanged accountId: %v", err)
		}
	})

	t.Run("setting accountId when old had none is accepted", func(t *testing.T) {
		if _, err := newPolicy(111).ValidateUpdate(newPolicy(0)); err != nil {
			t.Fatalf("unexpected error when old accountId was unset: %v", err)
		}
	})

	t.Run("new value from operator default carries hint", func(t *testing.T) {
		setDefaultAccountIDForTest(t, 222)
		_, err := newPolicy(222).ValidateUpdate(newPolicy(111))
		if err == nil {
			t.Fatalf("expected an error when accountId changes")
		}
		if !strings.Contains(err.Error(), "operator default") || !strings.Contains(err.Error(), "spec.accountId: 111") {
			t.Errorf("error %q missing the operator-default hint", err.Error())
		}
	})
}
