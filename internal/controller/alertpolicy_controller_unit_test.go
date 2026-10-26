package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	nrerrors "github.com/newrelic/newrelic-client-go/v2/pkg/errors"
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	existingPolicy := &alerts.AlertsPolicy{
		ID:                 "7345070",
		Name:               "AAA-2026-Policy-K8s",
		IncidentPreference: alerts.AlertsIncidentPreference(alertsv1.IncidentPreferencePerCondition),
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

	existingPolicy := &alerts.AlertsPolicy{
		ID:                 "7345070",
		Name:               "AAA-2026-Policy-K8s",
		IncidentPreference: alerts.AlertsIncidentPreference(alertsv1.IncidentPreferencePerCondition),
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

func TestHandleConditionsRecordsAppliedStateForSucceedingConditionDespiteSiblingFailure(t *testing.T) {
	updatingCondition := withoutConditionMetadata(testPolicyCondition("Temp1", "SELECT count(*) FROM Transaction"))
	failingCondition := withoutConditionMetadata(testPolicyCondition("Temp2", "SELECT count( FROM SyntheticCheck"))

	remoteBeforeUpdate := testRemoteCondition(updatingCondition)
	remoteBeforeUpdate.RunbookURL = "https://old.example.com" // force a spec mismatch so Temp1 takes the update path

	// AppliedSpec starts empty, mirroring a policy whose prior reconcile failed before the status snapshot ran
	policy := testAlertPolicyForConditions([]alertsv1.PolicyCondition{updatingCondition, failingCondition}, nil)

	updatedRemote := testRemoteCondition(updatingCondition)
	creationErr := errors.New("nrql.query: Unable to parse NRQL query")

	attempted := []string{}
	reconciler := &AlertPolicyReconciler{
		searchNrqlConditionsFn: func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
			return []*alerts.NrqlAlertCondition{remoteBeforeUpdate}, nil
		},
		updateStaticConditionFn: func(_ int, _ string, updateInput alerts.NrqlConditionUpdateInput) (*alerts.NrqlAlertCondition, error) {
			attempted = append(attempted, updateInput.Name)
			return updatedRemote, nil
		},
		createStaticConditionFn: func(_ int, _ string, createInput alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error) {
			attempted = append(attempted, createInput.Name)
			return nil, creationErr
		},
	}

	err := reconciler.handleConditions(policy)
	if err == nil {
		t.Fatalf("expected an error when a sibling condition fails to create, got nil")
	}

	if !errors.Is(err, creationErr) {
		t.Fatalf("expected returned error to wrap the underlying creation error, got: %v", err)
	}

	if len(attempted) != 2 {
		t.Fatalf("expected both conditions to be attempted despite the second failing, got %v", attempted)
	}

	if policy.Status.AppliedSpec == nil {
		t.Fatalf("expected AppliedSpec to be persisted even though a sibling condition failed")
	}

	idx := findAppliedConditionIndex(policy.Status.AppliedSpec.Conditions, "Temp1")
	if idx < 0 {
		t.Fatalf("expected successfully updated condition Temp1 to be recorded in AppliedSpec, got %+v", policy.Status.AppliedSpec.Conditions)
	}

	if policy.Status.AppliedSpec.Conditions[idx].ID != updatedRemote.ID {
		t.Fatalf("expected applied condition ID %q, got %q", updatedRemote.ID, policy.Status.AppliedSpec.Conditions[idx].ID)
	}

	// Temp2 failed to create - it must not carry a remote ID, otherwise it would be
	// mistaken for a successfully applied condition on the next reconcile.
	if failedIdx := findAppliedConditionIndex(policy.Status.AppliedSpec.Conditions, "Temp2"); failedIdx >= 0 {
		if policy.Status.AppliedSpec.Conditions[failedIdx].ID != "" {
			t.Fatalf("expected failing condition Temp2 to have no applied remote ID, got %q", policy.Status.AppliedSpec.Conditions[failedIdx].ID)
		}
	}
}

func TestCreatePolicyConditionsReturnsErrorOnConditionCreationFailure(t *testing.T) {
	failingCondition := withoutConditionMetadata(testPolicyCondition("Failing Condition", "SELECT count(*) FROM A"))
	succeedingCondition := withoutConditionMetadata(testPolicyCondition("Succeeding Condition", "SELECT count(*) FROM B"))
	policy := testAlertPolicyForConditions(
		[]alertsv1.PolicyCondition{failingCondition, succeedingCondition},
		[]alertsv1.PolicyCondition{failingCondition, succeedingCondition},
	)
	remoteSucceeding := testRemoteCondition(succeedingCondition)
	creationErr := errors.New("account has reached the limit of 6000 alert conditions")

	attempted := []string{}
	reconciler := &AlertPolicyReconciler{
		createStaticConditionFn: func(_ int, _ string, createInput alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error) {
			attempted = append(attempted, createInput.Name)
			if createInput.Name == failingCondition.Spec.Name {
				return nil, creationErr
			}
			return remoteSucceeding, nil
		},
	}

	err := reconciler.createPolicyConditions(policy)
	if err == nil {
		t.Fatalf("expected an error when a condition fails to create, got nil")
	}

	if !errors.Is(err, creationErr) {
		t.Fatalf("expected returned error to wrap the underlying creation error, got: %v", err)
	}

	if len(attempted) != 2 {
		t.Fatalf("expected both conditions to be attempted despite the first failing, got %v", attempted)
	}

	if policy.Status.AppliedSpec.Conditions[1].ID != remoteSucceeding.ID {
		t.Fatalf("expected succeeding condition to still be recorded as applied, got %+v", policy.Status.AppliedSpec.Conditions[1])
	}
}

func TestNrqlConditionValidationErrorsExtractsFieldDetail(t *testing.T) {
	body := []byte(`{"errors":[{"message":"Validation Error","extensions":{"code":"BAD_REQUEST","errorClass":"CLIENT_ERROR","validationErrors":[{"name":"nrql.query","reason":"must not be blank"}]}}]}`)
	gqlErr := &alerts.GraphQLErrorResponse{}
	if err := json.Unmarshal(body, gqlErr); err != nil {
		t.Fatalf("failed to unmarshal fixture: %v", err)
	}

	details := nrqlConditionValidationErrors(gqlErr)
	if len(details) != 1 || details[0] != "nrql.query: must not be blank" {
		t.Fatalf("expected extracted validation detail, got %v", details)
	}
}

func TestNrqlConditionValidationErrorsReturnsNilForOtherErrors(t *testing.T) {
	if details := nrqlConditionValidationErrors(errors.New("boom")); details != nil {
		t.Fatalf("expected nil for a non-GraphQL error, got %v", details)
	}
}

// fakePolicyAPI records the NerdGraph policy calls a reconciler makes and returns canned results.
type fakePolicyAPI struct {
	queryResult  *alerts.AlertsPolicy
	queryErr     error
	searchResult []*alerts.AlertsPolicy
	searchErr    error
	createResult *alerts.AlertsPolicy
	createErr    error
	updateResult *alerts.AlertsPolicy
	updateErr    error
	deleteErr    error

	queryCalls  []fakePolicyCall
	searchCalls []int
	createCalls []fakePolicyCall
	updateCalls []fakePolicyCall
	deleteCalls []fakePolicyCall
}

type fakePolicyCall struct {
	accountID int
	policyID  string
	name      string
	pref      alerts.AlertsIncidentPreference
}

func (f *fakePolicyAPI) reconciler() *AlertPolicyReconciler {
	return &AlertPolicyReconciler{
		queryPolicyFn: func(accountID int, id string) (*alerts.AlertsPolicy, error) {
			f.queryCalls = append(f.queryCalls, fakePolicyCall{accountID: accountID, policyID: id})
			return f.queryResult, f.queryErr
		},
		searchPoliciesFn: func(accountID int, _ alerts.AlertsPoliciesSearchCriteriaInput) ([]*alerts.AlertsPolicy, error) {
			f.searchCalls = append(f.searchCalls, accountID)
			return f.searchResult, f.searchErr
		},
		createPolicyFn: func(accountID int, input alerts.AlertsPolicyInput) (*alerts.AlertsPolicy, error) {
			f.createCalls = append(f.createCalls, fakePolicyCall{accountID: accountID, name: input.Name, pref: input.IncidentPreference})
			return f.createResult, f.createErr
		},
		updatePolicyFn: func(accountID int, id string, input alerts.AlertsPolicyUpdateInput) (*alerts.AlertsPolicy, error) {
			f.updateCalls = append(f.updateCalls, fakePolicyCall{accountID: accountID, policyID: id, name: input.Name, pref: input.IncidentPreference})
			if f.updateResult != nil || f.updateErr != nil {
				return f.updateResult, f.updateErr
			}
			return &alerts.AlertsPolicy{AccountID: accountID, ID: id, Name: input.Name, IncidentPreference: input.IncidentPreference}, nil
		},
		deletePolicyFn: func(accountID int, id string) error {
			f.deleteCalls = append(f.deleteCalls, fakePolicyCall{accountID: accountID, policyID: id})
			return f.deleteErr
		},
	}
}

func (f *fakePolicyAPI) totalCalls() int {
	return len(f.queryCalls) + len(f.searchCalls) + len(f.createCalls) + len(f.updateCalls) + len(f.deleteCalls)
}

// testManagedPolicy returns a policy that is already applied: status ID 100, spec and applied spec in sync.
func testManagedPolicy() *alertsv1.AlertPolicy {
	spec := alertsv1.AlertPolicySpec{
		Name:               "my-policy",
		IncidentPreference: alertsv1.IncidentPreferencePerCondition,
		Region:             "US",
		AccountID:          4242,
		APIKey:             "api-key",
	}

	return &alertsv1.AlertPolicy{
		Spec: spec,
		Status: alertsv1.AlertPolicyStatus{
			PolicyID:    100,
			AppliedSpec: spec.DeepCopy(),
		},
	}
}

func testRemotePolicy(id string, accountID int, name string) *alerts.AlertsPolicy {
	return &alerts.AlertsPolicy{
		AccountID:          accountID,
		ID:                 id,
		Name:               name,
		IncidentPreference: alerts.AlertsIncidentPreference(alertsv1.IncidentPreferencePerCondition),
	}
}

func requireReady(t *testing.T, policy *alertsv1.AlertPolicy, status metav1.ConditionStatus, reason string) {
	t.Helper()

	cond := meta.FindStatusCondition(policy.Status.Conditions, alertsv1.AlertPolicyConditionReady)
	if cond == nil {
		t.Fatalf("expected Ready condition to be set, got none")
	}
	if cond.Status != status || cond.Reason != reason {
		t.Fatalf("expected Ready=%s reason %s, got Ready=%s reason %s (%s)", status, reason, cond.Status, cond.Reason, cond.Message)
	}
}

func TestSyncRemotePolicyLooksUpByStatusIDNotName(t *testing.T) {
	policy := testManagedPolicy()
	fake := &fakePolicyAPI{queryResult: testRemotePolicy("100", 4242, "my-policy")}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncDone {
		t.Fatalf("expected syncDone with no error, got %v, %v", outcome, err)
	}

	if len(fake.searchCalls) != 0 {
		t.Fatalf("expected no name search when status has a policy ID, got %v", fake.searchCalls)
	}
	if len(fake.queryCalls) != 1 || fake.queryCalls[0].accountID != 4242 || fake.queryCalls[0].policyID != "100" {
		t.Fatalf("expected one query for policy 100 in account 4242, got %+v", fake.queryCalls)
	}
	requireReady(t, policy, metav1.ConditionTrue, alertsv1.AlertPolicyReasonSynced)
}

func TestSyncRemotePolicyDoesNotAdoptForeignSameNamePolicyWhenIDSet(t *testing.T) {
	policy := testManagedPolicy()
	policy.Spec.IncidentPreference = alertsv1.IncidentPreferencePerPolicy
	fake := &fakePolicyAPI{
		queryResult:  testRemotePolicy("100", 4242, "my-policy"),
		searchResult: []*alerts.AlertsPolicy{testRemotePolicy("999", 7, "my-policy")},
	}

	if _, err := fake.reconciler().syncRemotePolicy(policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if policy.Status.PolicyID != 100 {
		t.Fatalf("expected policy to stay bound to ID 100, got %d", policy.Status.PolicyID)
	}
	if len(fake.updateCalls) != 1 || fake.updateCalls[0].policyID != "100" {
		t.Fatalf("expected the update to target policy 100, got %+v", fake.updateCalls)
	}
	if len(fake.searchCalls) != 0 || len(fake.createCalls) != 0 || len(fake.deleteCalls) != 0 {
		t.Fatalf("expected no search, create or delete calls, got %+v", fake)
	}
}

func TestSyncRemotePolicyAdoptsSingleNameMatchWhenNoID(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status = alertsv1.AlertPolicyStatus{}
	fake := &fakePolicyAPI{searchResult: []*alerts.AlertsPolicy{
		testRemotePolicy("55", 4242, "other-policy"),
		testRemotePolicy("56", 4242, "my-policy"),
	}}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncDone {
		t.Fatalf("expected syncDone with no error, got %v, %v", outcome, err)
	}

	if policy.Status.PolicyID != 56 {
		t.Fatalf("expected policy 56 to be adopted, got %d", policy.Status.PolicyID)
	}
	if policy.Status.AppliedSpec == nil || policy.Status.AppliedSpec.Name != "my-policy" {
		t.Fatalf("expected applied spec to be recorded, got %+v", policy.Status.AppliedSpec)
	}
	if len(fake.searchCalls) != 1 || fake.searchCalls[0] != 4242 {
		t.Fatalf("expected one search in account 4242, got %v", fake.searchCalls)
	}
	if len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 {
		t.Fatalf("expected adoption without create or update, got %+v", fake)
	}
	requireReady(t, policy, metav1.ConditionTrue, alertsv1.AlertPolicyReasonSynced)
}

func TestSyncRemotePolicyIgnoresNonMatchingNamesAndCreates(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status = alertsv1.AlertPolicyStatus{}
	fake := &fakePolicyAPI{
		searchResult: []*alerts.AlertsPolicy{
			testRemotePolicy("55", 4242, "my-policy-2"),
			testRemotePolicy("56", 4242, "My-Policy"),
			testRemotePolicy("57", 7, "my-policy"),
		},
		createResult: testRemotePolicy("200", 4242, "my-policy"),
	}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncDone {
		t.Fatalf("expected syncDone with no error, got %v, %v", outcome, err)
	}

	if len(fake.createCalls) != 1 || fake.createCalls[0].accountID != 4242 || fake.createCalls[0].name != "my-policy" {
		t.Fatalf("expected one create in account 4242, got %+v", fake.createCalls)
	}
	if policy.Status.PolicyID != 200 {
		t.Fatalf("expected created policy ID 200, got %d", policy.Status.PolicyID)
	}
	if !(&AlertPolicyReconciler{}).matchesAppliedPolicyState(policy) {
		t.Fatalf("expected applied state to match after create")
	}
}

func TestSyncRemotePolicyAmbiguousNameBlocks(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status = alertsv1.AlertPolicyStatus{}
	fake := &fakePolicyAPI{searchResult: []*alerts.AlertsPolicy{
		testRemotePolicy("55", 4242, "my-policy"),
		testRemotePolicy("56", 4242, "my-policy"),
	}}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncBlocked {
		t.Fatalf("expected syncBlocked with no error, got %v, %v", outcome, err)
	}

	requireReady(t, policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAmbiguousPolicyName)
	if policy.Status.PolicyID != 0 || len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 {
		t.Fatalf("expected nothing to be adopted or changed, got ID %d, %+v", policy.Status.PolicyID, fake)
	}
}

func TestSyncRemotePolicyRecreatesWhenRemoteDeleted(t *testing.T) {
	policy := testManagedPolicy()
	staleCondition := testPolicyCondition("Example NRQL Static Condition", "SELECT count(*) FROM Transaction")
	policy.Spec.Conditions = []alertsv1.PolicyCondition{staleCondition}
	policy.Status.AppliedSpec.Conditions = []alertsv1.PolicyCondition{staleCondition}

	fake := &fakePolicyAPI{
		queryErr:     nrerrors.NewNotFound("policy not found"),
		searchResult: []*alerts.AlertsPolicy{testRemotePolicy("999", 4242, "my-policy")},
		createResult: testRemotePolicy("200", 4242, "my-policy"),
	}
	reconciler := fake.reconciler()

	newCondition := testRemoteCondition(withoutConditionMetadata(staleCondition))
	newCondition.ID = "new-condition-id"
	reconciler.createStaticConditionFn = func(_ int, policyID string, _ alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error) {
		if policyID != "200" {
			t.Fatalf("expected the condition to be created under the new policy 200, got %s", policyID)
		}
		return newCondition, nil
	}

	outcome, err := reconciler.syncRemotePolicy(policy)
	if err != nil || outcome != syncDone {
		t.Fatalf("expected syncDone with no error, got %v, %v", outcome, err)
	}

	if len(fake.createCalls) != 1 {
		t.Fatalf("expected the policy to be recreated, got %+v", fake.createCalls)
	}
	if len(fake.searchCalls) != 0 {
		t.Fatalf("expected no name lookup after the ID lookup came back not found, got %v", fake.searchCalls)
	}
	if policy.Status.PolicyID != 200 {
		t.Fatalf("expected new policy ID 200, got %d", policy.Status.PolicyID)
	}
	if got := policy.Spec.Conditions[0].ID; got != "new-condition-id" {
		t.Fatalf("expected stale condition ID to be replaced by the new one, got %q", got)
	}
	if got := policy.Status.AppliedSpec.Conditions[0].ID; got != "new-condition-id" {
		t.Fatalf("expected applied condition ID to be the new one, got %q", got)
	}
}

func TestSyncRemotePolicyNilOrEmptyQueryResultIsTransientError(t *testing.T) {
	tests := map[string]*alerts.AlertsPolicy{
		"nil policy": nil,
		"empty ID":   {AccountID: 4242, Name: "my-policy"},
	}

	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			policy := testManagedPolicy()
			fake := &fakePolicyAPI{queryResult: result}

			outcome, err := fake.reconciler().syncRemotePolicy(policy)
			if err == nil {
				t.Fatalf("expected an error for an empty lookup result")
			}
			if outcome == syncBlocked {
				t.Fatalf("expected an empty lookup result not to be treated as blocked")
			}
			if len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 || len(fake.searchCalls) != 0 {
				t.Fatalf("expected no create, update or search calls, got %+v", fake)
			}
			if policy.Status.PolicyID != 100 {
				t.Fatalf("expected policy ID to be kept, got %d", policy.Status.PolicyID)
			}
		})
	}
}

func TestSyncRemotePolicyAccountAccessDeniedBlocks(t *testing.T) {
	tests := map[string]error{
		"typed unauthorized": nrerrors.NewUnauthorizedError(),
		"graphql message":    errors.New("User does not have access to account 4242"),
	}

	for name, queryErr := range tests {
		t.Run(name, func(t *testing.T) {
			policy := testManagedPolicy()
			policy.Spec.Name = "renamed"
			fake := &fakePolicyAPI{queryErr: queryErr}

			outcome, err := fake.reconciler().syncRemotePolicy(policy)
			if err != nil || outcome != syncBlocked {
				t.Fatalf("expected syncBlocked with no error, got %v, %v", outcome, err)
			}

			requireReady(t, policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountAccessDenied)
			if len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 || len(fake.searchCalls) != 0 {
				t.Fatalf("expected no further calls once access is denied, got %+v", fake)
			}
			if policy.Status.PolicyID != 100 {
				t.Fatalf("expected policy ID to be kept, got %d", policy.Status.PolicyID)
			}
		})
	}
}

func TestSyncRemotePolicyAccountMismatchBlocks(t *testing.T) {
	policy := testManagedPolicy()
	policy.Spec.Name = "renamed"
	fake := &fakePolicyAPI{queryResult: testRemotePolicy("100", 7, "my-policy")}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncBlocked {
		t.Fatalf("expected syncBlocked with no error, got %v, %v", outcome, err)
	}

	requireReady(t, policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountMismatch)
	if len(fake.updateCalls) != 0 || len(fake.createCalls) != 0 {
		t.Fatalf("expected no changes for a policy in another account, got %+v", fake)
	}
}

func TestSyncRemotePolicyAccountIDChangedBlocks(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status.AppliedSpec.AccountID = 5
	fake := &fakePolicyAPI{}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncBlocked {
		t.Fatalf("expected syncBlocked with no error, got %v, %v", outcome, err)
	}

	requireReady(t, policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountIDChanged)
	if fake.totalCalls() != 0 {
		t.Fatalf("expected no API calls before blocking, got %+v", fake)
	}
}

func TestSyncRemotePolicyUpdateRefreshesAppliedSpecName(t *testing.T) {
	policy := testManagedPolicy()
	policy.Spec.Name = "new-name"
	fake := &fakePolicyAPI{queryResult: testRemotePolicy("100", 4242, "my-policy")}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if err != nil || outcome != syncDone {
		t.Fatalf("expected syncDone with no error, got %v, %v", outcome, err)
	}

	if len(fake.updateCalls) != 1 {
		t.Fatalf("expected one update, got %+v", fake.updateCalls)
	}
	update := fake.updateCalls[0]
	if update.accountID != 4242 || update.policyID != "100" || update.name != "new-name" {
		t.Fatalf("unexpected update call %+v", update)
	}
	if policy.Status.AppliedSpec.Name != "new-name" {
		t.Fatalf("expected applied name to be refreshed, got %q", policy.Status.AppliedSpec.Name)
	}
	if !(&AlertPolicyReconciler{}).matchesAppliedPolicyState(policy) {
		t.Fatalf("expected applied state to match after a rename, otherwise the rename would loop")
	}
	if len(fake.createCalls) != 0 {
		t.Fatalf("expected a rename to update in place, not create, got %+v", fake.createCalls)
	}
}

func TestSyncRemotePolicyRefreshesAppliedSpecWhenRemoteUnchanged(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status.AppliedSpec.APIKey = "old-key"
	fake := &fakePolicyAPI{queryResult: testRemotePolicy("100", 4242, "my-policy")}

	if _, err := fake.reconciler().syncRemotePolicy(policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.updateCalls) != 0 {
		t.Fatalf("expected no update when the remote policy already matches, got %+v", fake.updateCalls)
	}
	if policy.Status.AppliedSpec.APIKey != "api-key" {
		t.Fatalf("expected applied apiKey to be refreshed, got %q", policy.Status.AppliedSpec.APIKey)
	}
	if !(&AlertPolicyReconciler{}).matchesAppliedPolicyState(policy) {
		t.Fatalf("expected applied state to match after the refresh")
	}
}

func TestSyncRemotePolicyPropagatesGenericError(t *testing.T) {
	policy := testManagedPolicy()
	boom := errors.New("boom: upstream timeout")
	fake := &fakePolicyAPI{queryErr: boom}

	outcome, err := fake.reconciler().syncRemotePolicy(policy)
	if !errors.Is(err, boom) {
		t.Fatalf("expected the generic error to be returned, got %v", err)
	}
	if outcome == syncBlocked {
		t.Fatalf("expected a generic error not to be treated as blocked")
	}
	if len(policy.Status.Conditions) != 0 {
		t.Fatalf("expected no condition to be set for a generic error, got %+v", policy.Status.Conditions)
	}
}

func TestCreatePolicyReturnsErrorOnNonNumericID(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status = alertsv1.AlertPolicyStatus{}
	fake := &fakePolicyAPI{createResult: testRemotePolicy("not-a-number", 4242, "my-policy")}

	err := fake.reconciler().createPolicy(policy)
	if err == nil {
		t.Fatalf("expected an error when the created policy ID is not numeric")
	}
	if !errors.Is(err, strconv.ErrSyntax) {
		t.Fatalf("expected the conversion error to be wrapped, got %v", err)
	}
	if policy.Status.PolicyID != 0 {
		t.Fatalf("expected no policy ID to be recorded, got %d", policy.Status.PolicyID)
	}
}

func TestDeletePolicySkipsWhenNoPolicyID(t *testing.T) {
	policy := testManagedPolicy()
	policy.Status = alertsv1.AlertPolicyStatus{}
	fake := &fakePolicyAPI{searchResult: []*alerts.AlertsPolicy{testRemotePolicy("999", 4242, "my-policy")}}

	if err := fake.reconciler().deletePolicy(policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.totalCalls() != 0 {
		t.Fatalf("expected no API calls without a policy ID, got %+v", fake)
	}
}

func TestDeletePolicyUsesAppliedSpecAccountID(t *testing.T) {
	policy := testManagedPolicy()
	policy.Spec.AccountID = 8
	policy.Status.AppliedSpec.AccountID = 7
	fake := &fakePolicyAPI{}

	if err := fake.reconciler().deletePolicy(policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0].accountID != 7 || fake.deleteCalls[0].policyID != "100" {
		t.Fatalf("expected delete of policy 100 in applied account 7, got %+v", fake.deleteCalls)
	}
	if len(fake.searchCalls) != 0 {
		t.Fatalf("expected delete never to search by name, got %v", fake.searchCalls)
	}
}

func TestDeletePolicyFallsBackToSpecAccountID(t *testing.T) {
	tests := map[string]*alertsv1.AlertPolicySpec{
		"no applied spec":         nil,
		"applied spec no account": {Name: "my-policy"},
	}

	for name, applied := range tests {
		t.Run(name, func(t *testing.T) {
			policy := testManagedPolicy()
			policy.Status.AppliedSpec = applied
			fake := &fakePolicyAPI{}

			if err := fake.reconciler().deletePolicy(policy); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fake.deleteCalls) != 1 || fake.deleteCalls[0].accountID != 4242 {
				t.Fatalf("expected delete in spec account 4242, got %+v", fake.deleteCalls)
			}
		})
	}
}

func TestDeletePolicyTypedNotFoundSucceedsWithoutConfirmQuery(t *testing.T) {
	fake := &fakePolicyAPI{deleteErr: fmt.Errorf("delete: %w", nrerrors.NewNotFound("policy missing"))}

	if err := fake.reconciler().deletePolicy(testManagedPolicy()); err != nil {
		t.Fatalf("expected a typed not found delete to succeed, got %v", err)
	}
	if len(fake.queryCalls) != 0 {
		t.Fatalf("expected no confirm query after a typed not found, got %+v", fake.queryCalls)
	}
}

func TestDeletePolicyUntypedErrorConfirmedGoneSucceeds(t *testing.T) {
	fake := &fakePolicyAPI{
		deleteErr: errors.New("Policy not found"),
		queryErr:  nrerrors.NewNotFound("policy missing"),
	}

	if err := fake.reconciler().deletePolicy(testManagedPolicy()); err != nil {
		t.Fatalf("expected success when the confirm query says the policy is gone, got %v", err)
	}
	if len(fake.queryCalls) != 1 || fake.queryCalls[0].accountID != 4242 || fake.queryCalls[0].policyID != "100" {
		t.Fatalf("expected one confirm query for policy 100 in account 4242, got %+v", fake.queryCalls)
	}
	if len(fake.searchCalls) != 0 {
		t.Fatalf("expected delete never to search by name, got %v", fake.searchCalls)
	}
}

func TestDeletePolicyUntypedErrorReturnedWhenPolicyStillExists(t *testing.T) {
	deleteErr := errors.New("Policy not found")
	fake := &fakePolicyAPI{
		deleteErr:   deleteErr,
		queryResult: testRemotePolicy("100", 4242, "my-policy"),
	}

	if err := fake.reconciler().deletePolicy(testManagedPolicy()); !errors.Is(err, deleteErr) {
		t.Fatalf("expected the original delete error so the finalizer is kept, got %v", err)
	}
}

func TestDeletePolicyUntypedErrorReturnedWhenConfirmQueryFails(t *testing.T) {
	deleteErr := errors.New("boom: upstream timeout")
	fake := &fakePolicyAPI{deleteErr: deleteErr, queryErr: errors.New("query also failed")}

	if err := fake.reconciler().deletePolicy(testManagedPolicy()); !errors.Is(err, deleteErr) {
		t.Fatalf("expected the original delete error, not the confirm query error, got %v", err)
	}
}

func TestIsAccountAccessError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed unauthorized", nrerrors.NewUnauthorizedError(), true},
		{"wrapped typed unauthorized", fmt.Errorf("lookup: %w", nrerrors.NewUnauthorizedError()), true},
		{"access denied", errors.New("Access Denied"), true},
		{"not authorized", errors.New("User is not authorized to view account 1"), true},
		{"forbidden", errors.New("403 Forbidden"), true},
		{"does not have access", errors.New("API key does not have access to account 1"), true},
		{"generic", errors.New("boom"), false},
		{"not found", nrerrors.NewNotFound("policy not found"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAccountAccessError(tt.err); got != tt.want {
				t.Fatalf("isAccountAccessError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsPolicyNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed not found", nrerrors.NewNotFound(""), true},
		{"wrapped typed not found", fmt.Errorf("lookup: %w", nrerrors.NewNotFound("gone")), true},
		{"plain string not found", errors.New("Policy not found"), false},
		{"generic", errors.New("boom"), false},
		{"unauthorized", nrerrors.NewUnauthorizedError(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPolicyNotFound(tt.err); got != tt.want {
				t.Fatalf("isPolicyNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
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
