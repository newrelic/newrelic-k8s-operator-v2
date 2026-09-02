package controller

import (
	"testing"

	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
)

func testNrqlConditionWithTerm(term alertsv1.AlertsNrqlConditionTerm) *alertsv1.NrqlCondition {
	return &alertsv1.NrqlCondition{
		Spec: alertsv1.NrqlConditionSpec{
			AlertsNrqlBaseSpec: alertsv1.AlertsNrqlBaseSpec{
				Terms: []alertsv1.AlertsNrqlConditionTerm{term},
			},
		},
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func TestTranslateNrqlCreateInputPropagatesDisableHealthStatusReporting(t *testing.T) {
	cases := map[string]*bool{
		"true":  boolPtr(true),
		"false": boolPtr(false),
		"nil":   nil,
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			cond := testNrqlConditionWithTerm(alertsv1.AlertsNrqlConditionTerm{
				Threshold:                    "5",
				DisableHealthStatusReporting: want,
			})

			r := &NrqlConditionReconciler{}
			input := r.translateNrqlCreateInput(cond)

			if len(input.Terms) != 1 {
				t.Fatalf("expected exactly one translated term, got %d", len(input.Terms))
			}

			got := input.Terms[0].DisableHealthStatusReporting
			if (want == nil) != (got == nil) {
				t.Fatalf("expected DisableHealthStatusReporting nil-ness %v, got %v", want == nil, got == nil)
			}
			if want != nil && *got != *want {
				t.Fatalf("expected DisableHealthStatusReporting %v, got %v", *want, *got)
			}
		})
	}
}

func TestTranslateNrqlUpdateInputPropagatesDisableHealthStatusReporting(t *testing.T) {
	cases := map[string]*bool{
		"true":  boolPtr(true),
		"false": boolPtr(false),
		"nil":   nil,
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			cond := testNrqlConditionWithTerm(alertsv1.AlertsNrqlConditionTerm{
				Threshold:                    "5",
				DisableHealthStatusReporting: want,
			})

			r := &NrqlConditionReconciler{}
			input := r.translateNrqlUpdateInput(cond)

			if len(input.Terms) != 1 {
				t.Fatalf("expected exactly one translated term, got %d", len(input.Terms))
			}

			got := input.Terms[0].DisableHealthStatusReporting
			if (want == nil) != (got == nil) {
				t.Fatalf("expected DisableHealthStatusReporting nil-ness %v, got %v", want == nil, got == nil)
			}
			if want != nil && *got != *want {
				t.Fatalf("expected DisableHealthStatusReporting %v, got %v", *want, *got)
			}
		})
	}
}
