package controller

import (
	"strconv"
	"testing"

	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
)

func TestBuildAppliedPolicySpecPreservesAppliedConditions(t *testing.T) {
	appliedCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction")
	desiredCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction WHERE appName = 'blah'")

	policy := &alertsv1.AlertPolicy{
		Spec: alertsv1.AlertPolicySpec{
			Name:               "AAA-2026-Policy-K8s",
			IncidentPreference: alertsv1.IncidentPreferencePerCondition,
			Region:             "US",
			AccountID:          1,
			APIKey:             "api-key",
			Conditions:         []alertsv1.PolicyCondition{desiredCondition},
		},
		Status: alertsv1.AlertPolicyStatus{
			AppliedSpec: &alertsv1.AlertPolicySpec{
				Conditions: []alertsv1.PolicyCondition{appliedCondition},
			},
		},
	}

	existingPolicy := &alerts.Policy{
		ID:                 7345070,
		Name:               "AAA-2026-Policy-K8s",
		IncidentPreference: alerts.IncidentPreferenceType(alertsv1.IncidentPreferencePerCondition),
	}

	appliedSpec := buildAppliedPolicySpec(policy, existingPolicy)

	if len(appliedSpec.Conditions) != 1 {
		t.Fatalf("expected exactly one preserved condition, got %d", len(appliedSpec.Conditions))
	}

	if appliedSpec.Conditions[0].ID != appliedCondition.ID {
		t.Fatalf("expected preserved condition ID %q, got %q", appliedCondition.ID, appliedSpec.Conditions[0].ID)
	}

	if appliedSpec.Conditions[0].Spec.Nrql.Query != appliedCondition.Spec.Nrql.Query {
		t.Fatalf("expected preserved applied query %q, got %q", appliedCondition.Spec.Nrql.Query, appliedSpec.Conditions[0].Spec.Nrql.Query)
	}

	if appliedSpec.Conditions[0].Spec.Nrql.Query == policy.Spec.Conditions[0].Spec.Nrql.Query {
		t.Fatalf("expected applied conditions to remain distinct from desired spec conditions")
	}

	appliedSpec.Conditions[0].Spec.Nrql.Query = "SELECT count(*) FROM SyntheticCheck"
	if policy.Status.AppliedSpec.Conditions[0].Spec.Nrql.Query != appliedCondition.Spec.Nrql.Query {
		t.Fatalf("expected buildAppliedPolicySpec to clone preserved conditions")
	}
	if appliedSpec.Name != existingPolicy.Name {
		t.Fatalf("expected remote policy name %q, got %q", existingPolicy.Name, appliedSpec.Name)
	}
}

func TestBuildAppliedPolicySpecDoesNotPromoteDesiredConditionsWithoutAppliedStatus(t *testing.T) {
	desiredCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction WHERE appName = 'blah'")

	policy := &alertsv1.AlertPolicy{
		Spec: alertsv1.AlertPolicySpec{
			Name:               "AAA-2026-Policy-K8s",
			IncidentPreference: alertsv1.IncidentPreferencePerCondition,
			Region:             "US",
			AccountID:          1,
			APIKey:             "api-key",
			Conditions:         []alertsv1.PolicyCondition{desiredCondition},
		},
		Status: alertsv1.AlertPolicyStatus{
			AppliedSpec: &alertsv1.AlertPolicySpec{},
		},
	}

	existingPolicy := &alerts.Policy{
		ID:                 7345070,
		Name:               "AAA-2026-Policy-K8s",
		IncidentPreference: alerts.IncidentPreferenceType(alertsv1.IncidentPreferencePerCondition),
	}

	appliedSpec := buildAppliedPolicySpec(policy, existingPolicy)

	if len(appliedSpec.Conditions) != 0 {
		t.Fatalf("expected no applied conditions to be synthesized from desired spec, got %d", len(appliedSpec.Conditions))
	}

	reconciler := &AlertPolicyReconciler{}
	policy.Status.AppliedSpec = appliedSpec
	if !reconciler.haveConditionsChanged(policy) {
		t.Fatalf("expected missing applied conditions to force condition reconciliation after upgrade")
	}
	if appliedSpec.Name != existingPolicy.Name {
		t.Fatalf("expected remote policy name %q, got %q", existingPolicy.Name, appliedSpec.Name)
	}
}

func TestFindAppliedConditionIndexMatchesByName(t *testing.T) {
	conditions := []alertsv1.PolicyCondition{
		{Name: "Example Nrql Baseline Condition222"},
		{Name: "Example NRQL Static Condition"},
	}

	if got := findAppliedConditionIndex(conditions, "Example NRQL Static Condition"); got != 1 {
		t.Fatalf("expected matching condition index 1, got %d", got)
	}

	if got := findAppliedConditionIndex(conditions, "missing"); got != -1 {
		t.Fatalf("expected missing condition index -1, got %d", got)
	}
}

func TestHaveConditionsChangedHandlesMissingAppliedSpec(t *testing.T) {
	reconciler := &AlertPolicyReconciler{}
	policy := &alertsv1.AlertPolicy{
		Spec: alertsv1.AlertPolicySpec{
			Conditions: []alertsv1.PolicyCondition{
				testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction WHERE appName = 'blah'"),
			},
		},
	}

	if !reconciler.haveConditionsChanged(policy) {
		t.Fatalf("expected conditions to be treated as changed when no applied status exists")
	}
}

func TestMatchesAppliedPolicyStateIgnoresConditionMetadata(t *testing.T) {
	reconciler := &AlertPolicyReconciler{}
	desiredCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction")
	desiredCondition.Name = ""
	desiredCondition.ID = ""

	appliedCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction")

	policy := &alertsv1.AlertPolicy{
		Spec: alertsv1.AlertPolicySpec{
			Name:               "AAA-2026-Policy-K8s",
			IncidentPreference: alertsv1.IncidentPreferencePerCondition,
			Region:             "US",
			AccountID:          1,
			APIKey:             "api-key",
			Conditions:         []alertsv1.PolicyCondition{desiredCondition},
		},
		Status: alertsv1.AlertPolicyStatus{
			PolicyID: 7345070,
			AppliedSpec: &alertsv1.AlertPolicySpec{
				Name:               "AAA-2026-Policy-K8s",
				IncidentPreference: alertsv1.IncidentPreferencePerCondition,
				Region:             "US",
				AccountID:          1,
				APIKey:             "api-key",
				Conditions:         []alertsv1.PolicyCondition{appliedCondition},
			},
		},
	}

	if !reconciler.matchesAppliedPolicyState(policy) {
		t.Fatalf("expected semantic applied-state match when only controller metadata differs")
	}
}

func TestMatchesRemoteConditionSpecTreatsEquivalentRemoteStateAsMatch(t *testing.T) {
	reconciler := &AlertPolicyReconciler{}
	desiredCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM SyntheticCheck")
	desiredCondition.Name = ""
	desiredCondition.ID = ""
	desiredCondition.Spec.Description = "test description"
	desiredCondition.Spec.Enabled = true
	desiredCondition.Spec.RunbookURL = "https://www.google.com"
	desiredCondition.Spec.Terms = []alertsv1.AlertsNrqlConditionTerm{{
		Operator:             alerts.AlertsNRQLConditionTermsOperator("BELOW"),
		Priority:             alerts.NrqlConditionPriorities.Critical,
		Threshold:            "70",
		ThresholdDuration:    60,
		ThresholdOccurrences: alerts.ThresholdOccurrences.All,
	}}

	remoteCondition := testRemoteCondition(desiredCondition)

	if !reconciler.matchesRemoteConditionSpec(remoteCondition, &desiredCondition) {
		t.Fatalf("expected remote condition to match desired spec")
	}
}

func TestMatchesRemoteConditionSpecDetectsRemoteDrift(t *testing.T) {
	reconciler := &AlertPolicyReconciler{}
	desiredCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM SyntheticCheck")
	desiredCondition.Name = ""
	desiredCondition.ID = ""

	remoteCondition := testRemoteCondition(desiredCondition)
	remoteCondition.Nrql.Query = "SELECT count(*) FROM SyntheticCheck WHERE monitorName = 'other'"

	if reconciler.matchesRemoteConditionSpec(remoteCondition, &desiredCondition) {
		t.Fatalf("expected remote condition mismatch when remote query drifts from desired spec")
	}
}

func TestShouldReconcileConditionsHandlesManagedDeletions(t *testing.T) {
	policy := &alertsv1.AlertPolicy{
		Status: alertsv1.AlertPolicyStatus{
			AppliedSpec: &alertsv1.AlertPolicySpec{
				Conditions: []alertsv1.PolicyCondition{testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction")},
			},
		},
	}

	if !shouldReconcileConditions(policy) {
		t.Fatalf("expected previously managed conditions to keep reconciliation enabled when desired conditions are empty")
	}
}

func TestNeedsConditionReconcileDetectsMissingRemoteCondition(t *testing.T) {
	desiredCondition := withoutConditionMetadata(testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction"))
	policy := testAlertPolicyForConditions([]alertsv1.PolicyCondition{desiredCondition}, []alertsv1.PolicyCondition{desiredCondition})

	reconciler := &AlertPolicyReconciler{
		searchNrqlConditionsFn: func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
			return []*alerts.NrqlAlertCondition{}, nil
		},
	}

	needsReconcile, err := reconciler.needsConditionReconcile(policy)
	if err != nil {
		t.Fatalf("expected remote drift detection to succeed, got error: %v", err)
	}

	if !needsReconcile {
		t.Fatalf("expected missing remote condition to force reconciliation")
	}
}

func TestHandleConditionsPreservesMetadataForUnchangedCondition(t *testing.T) {
	remoteSource := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction")
	desiredCondition := withoutConditionMetadata(remoteSource)
	appliedCondition := withoutConditionMetadata(remoteSource)
	policy := testAlertPolicyForConditions([]alertsv1.PolicyCondition{desiredCondition}, []alertsv1.PolicyCondition{appliedCondition})
	remoteCondition := testRemoteCondition(remoteSource)

	reconciler := &AlertPolicyReconciler{
		searchNrqlConditionsFn: func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
			return []*alerts.NrqlAlertCondition{remoteCondition}, nil
		},
	}

	if err := reconciler.handleConditions(policy); err != nil {
		t.Fatalf("expected unchanged condition reconciliation to succeed, got error: %v", err)
	}

	if policy.Status.AppliedSpec == nil || len(policy.Status.AppliedSpec.Conditions) != 1 {
		t.Fatalf("expected exactly one applied condition after reconciliation")
	}

	if policy.Status.AppliedSpec.Conditions[0].Name != remoteCondition.Name {
		t.Fatalf("expected applied condition name %q, got %q", remoteCondition.Name, policy.Status.AppliedSpec.Conditions[0].Name)
	}

	if policy.Status.AppliedSpec.Conditions[0].ID != remoteCondition.ID {
		t.Fatalf("expected applied condition ID %q, got %q", remoteCondition.ID, policy.Status.AppliedSpec.Conditions[0].ID)
	}
}

func TestHandleConditionsDeletesRemovedConditionWithRecoveredRemoteID(t *testing.T) {
	remoteA := testRemoteCondition(testPolicyCondition("Example Nrql Baseline Condition", "SELECT count(*) FROM Transaction"))
	remoteB := testRemoteCondition(testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM SyntheticCheck"))
	policy := testAlertPolicyForConditions(
		[]alertsv1.PolicyCondition{withoutConditionMetadata(testPolicyCondition(remoteA.Name, remoteA.Nrql.Query))},
		[]alertsv1.PolicyCondition{
			withoutConditionMetadata(testPolicyCondition(remoteA.Name, remoteA.Nrql.Query)),
			withoutConditionMetadata(testPolicyCondition(remoteB.Name, remoteB.Nrql.Query)),
		},
	)

	deletedIDs := []string{}
	reconciler := &AlertPolicyReconciler{
		searchNrqlConditionsFn: func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
			return []*alerts.NrqlAlertCondition{remoteA, remoteB}, nil
		},
		deleteConditionFn: func(accountID int, conditionID string) error {
			deletedIDs = append(deletedIDs, conditionID)
			return nil
		},
	}

	if err := reconciler.handleConditions(policy); err != nil {
		t.Fatalf("expected condition removal reconciliation to succeed, got error: %v", err)
	}

	if len(deletedIDs) != 1 || deletedIDs[0] != remoteB.ID {
		t.Fatalf("expected deleted condition ID %q, got %v", remoteB.ID, deletedIDs)
	}

	if policy.Status.AppliedSpec == nil || len(policy.Status.AppliedSpec.Conditions) != 1 {
		t.Fatalf("expected one remaining applied condition after deletion")
	}

	if policy.Status.AppliedSpec.Conditions[0].ID != remoteA.ID {
		t.Fatalf("expected remaining applied condition ID %q, got %q", remoteA.ID, policy.Status.AppliedSpec.Conditions[0].ID)
	}
}

func TestHandleConditionsDeletesAllWhenDesiredConditionsEmpty(t *testing.T) {
	remoteCondition := testRemoteCondition(testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction"))
	policy := testAlertPolicyForConditions(nil, []alertsv1.PolicyCondition{withoutConditionMetadata(testPolicyCondition(remoteCondition.Name, remoteCondition.Nrql.Query))})

	deletedIDs := []string{}
	reconciler := &AlertPolicyReconciler{
		searchNrqlConditionsFn: func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
			return []*alerts.NrqlAlertCondition{remoteCondition}, nil
		},
		deleteConditionFn: func(accountID int, conditionID string) error {
			deletedIDs = append(deletedIDs, conditionID)
			return nil
		},
	}

	if err := reconciler.handleConditions(policy); err != nil {
		t.Fatalf("expected empty desired conditions to delete managed remote conditions, got error: %v", err)
	}

	if len(deletedIDs) != 1 || deletedIDs[0] != remoteCondition.ID {
		t.Fatalf("expected deleted condition ID %q, got %v", remoteCondition.ID, deletedIDs)
	}

	if policy.Status.AppliedSpec == nil || len(policy.Status.AppliedSpec.Conditions) != 0 {
		t.Fatalf("expected applied conditions to be empty after delete-all reconcile")
	}
}

func TestHandleConditionsRecreatesRemoteDeletedConditionWithoutStaleDelete(t *testing.T) {
	desiredCondition := withoutConditionMetadata(testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction"))
	policy := testAlertPolicyForConditions([]alertsv1.PolicyCondition{desiredCondition}, []alertsv1.PolicyCondition{desiredCondition})
	recreatedCondition := testRemoteCondition(testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction"))

	searchCalls := 0
	deletedIDs := []string{}
	reconciler := &AlertPolicyReconciler{
		searchNrqlConditionsFn: func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
			searchCalls++
			if searchCalls == 1 {
				return []*alerts.NrqlAlertCondition{}, nil
			}

			return []*alerts.NrqlAlertCondition{recreatedCondition}, nil
		},
		createStaticConditionFn: func(int, string, alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error) {
			return recreatedCondition, nil
		},
		deleteConditionFn: func(accountID int, conditionID string) error {
			deletedIDs = append(deletedIDs, conditionID)
			return nil
		},
	}

	if err := reconciler.handleConditions(policy); err != nil {
		t.Fatalf("expected missing remote condition to be recreated without cleanup failure, got error: %v", err)
	}

	if len(deletedIDs) != 0 {
		t.Fatalf("expected recreated condition to avoid stale delete attempts, got deletes %v", deletedIDs)
	}

	if policy.Status.AppliedSpec == nil || len(policy.Status.AppliedSpec.Conditions) != 1 {
		t.Fatalf("expected one recreated applied condition after reconciliation")
	}

	if policy.Status.AppliedSpec.Conditions[0].ID != recreatedCondition.ID {
		t.Fatalf("expected recreated condition ID %q, got %q", recreatedCondition.ID, policy.Status.AppliedSpec.Conditions[0].ID)
	}
}

func testPolicyCondition(name string, query string) alertsv1.PolicyCondition {
	return alertsv1.PolicyCondition{
		Name: name,
		ID:   name + "-id",
		Spec: alertsv1.PolicyConditionSpec{
			NrqlConditionSpec: alertsv1.NrqlConditionSpec{
				AlertsNrqlBaseSpec: alertsv1.AlertsNrqlBaseSpec{
					Name: name,
					Type: alerts.NrqlConditionTypes.Static,
					Nrql: alertsv1.NrqlConditionCreateQuery{
						Query: query,
					},
				},
			},
		},
	}
}

func testAlertPolicyForConditions(desiredConditions []alertsv1.PolicyCondition, appliedConditions []alertsv1.PolicyCondition) *alertsv1.AlertPolicy {
	return &alertsv1.AlertPolicy{
		Spec: alertsv1.AlertPolicySpec{
			AccountID:  1,
			Conditions: desiredConditions,
		},
		Status: alertsv1.AlertPolicyStatus{
			PolicyID: 7345070,
			AppliedSpec: &alertsv1.AlertPolicySpec{
				Conditions: appliedConditions,
			},
		},
	}
}

func withoutConditionMetadata(condition alertsv1.PolicyCondition) alertsv1.PolicyCondition {
	condition.Name = ""
	condition.ID = ""
	return condition
}

func testRemoteCondition(condition alertsv1.PolicyCondition) *alerts.NrqlAlertCondition {
	remoteCondition := &alerts.NrqlAlertCondition{
		NrqlConditionBase: alerts.NrqlConditionBase{
			Description:               condition.Spec.Description,
			Enabled:                   condition.Spec.Enabled,
			Name:                      condition.Spec.Name,
			Nrql:                      alerts.NrqlConditionQuery{Query: condition.Spec.Nrql.Query, DataAccountId: condition.Spec.Nrql.DataAccountID, EvaluationOffset: condition.Spec.Nrql.EvaluationOffset},
			RunbookURL:                condition.Spec.RunbookURL,
			Type:                      condition.Spec.Type,
			ViolationTimeLimit:        condition.Spec.ViolationTimeLimit,
			ViolationTimeLimitSeconds: condition.Spec.ViolationTimeLimitSeconds,
			TitleTemplate:             condition.Spec.TitleTemplate,
		},
		ID:                condition.ID,
		PolicyID:          "7345070",
		BaselineDirection: condition.Spec.BaselineDirection,
	}

	if condition.Spec.Expiration != nil {
		remoteCondition.Expiration = &alerts.AlertsNrqlConditionExpiration{
			ExpirationDuration:          condition.Spec.Expiration.ExpirationDuration,
			CloseViolationsOnExpiration: condition.Spec.Expiration.CloseViolationsOnExpiration,
			OpenViolationOnExpiration:   condition.Spec.Expiration.OpenViolationOnExpiration,
			IgnoreOnExpectedTermination: condition.Spec.Expiration.IgnoreOnExpectedTermination,
		}
	}

	if condition.Spec.Signal != nil {
		remoteCondition.Signal = &alerts.AlertsNrqlConditionSignal{
			AggregationWindow: condition.Spec.Signal.AggregationWindow,
			EvaluationOffset:  condition.Spec.Signal.EvaluationOffset,
			EvaluationDelay:   condition.Spec.Signal.EvaluationDelay,
			FillOption:        condition.Spec.Signal.FillOption,
			FillValue:         condition.Spec.Signal.FillValue,
			AggregationMethod: condition.Spec.Signal.AggregationMethod,
			AggregationDelay:  condition.Spec.Signal.AggregationDelay,
			AggregationTimer:  condition.Spec.Signal.AggregationTimer,
			SlideBy:           condition.Spec.Signal.SlideBy,
		}
	}

	for _, term := range condition.Spec.Terms {
		threshold, err := strconv.ParseFloat(term.Threshold, 64)
		if err != nil {
			panic(err)
		}

		remoteCondition.Terms = append(remoteCondition.Terms, alerts.NrqlConditionTerm{
			Operator:             term.Operator,
			Priority:             term.Priority,
			Threshold:            &threshold,
			ThresholdDuration:    term.ThresholdDuration,
			ThresholdOccurrences: term.ThresholdOccurrences,
		})
	}

	return remoteCondition
}
