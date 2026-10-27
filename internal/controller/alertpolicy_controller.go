/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	nrerrors "github.com/newrelic/newrelic-client-go/v2/pkg/errors"
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
	"github.com/newrelic/newrelic-k8s-operator-v2/interfaces"
)

// blockedRequeueInterval is how long to wait before retrying a policy that cannot progress until
// something outside the operator changes (key access, same-name policies, account mismatch).
const blockedRequeueInterval = 10 * time.Minute

// syncOutcome reports whether syncRemotePolicy finished or stopped on a condition that needs outside action.
type syncOutcome int

const (
	syncDone syncOutcome = iota
	syncBlocked
)

// accountAccessErrorSubstrings are lower-cased fragments of NerdGraph error messages that mean the
// API key cannot access the requested account. The client returns no typed error for these.
var accountAccessErrorSubstrings = []string{
	"access denied",
	"not authorized",
	"forbidden",
	"does not have access",
}

// AlertPolicyReconciler reconciles a AlertPolicy object
type AlertPolicyReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	Log                       logr.Logger
	Alerts                    interfaces.NewRelicClientInterface
	AlertClient               func(string, string) (interfaces.NewRelicClientInterface, error)
	searchNrqlConditionsFn    func(int, alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error)
	createStaticConditionFn   func(int, string, alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error)
	createBaselineConditionFn func(int, string, alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error)
	updateStaticConditionFn   func(int, string, alerts.NrqlConditionUpdateInput) (*alerts.NrqlAlertCondition, error)
	updateBaselineConditionFn func(int, string, alerts.NrqlConditionUpdateInput) (*alerts.NrqlAlertCondition, error)
	deleteConditionFn         func(int, string) error
	queryPolicyFn             func(int, string) (*alerts.AlertsPolicy, error)
	searchPoliciesFn          func(int, alerts.AlertsPoliciesSearchCriteriaInput) ([]*alerts.AlertsPolicy, error)
	createPolicyFn            func(int, alerts.AlertsPolicyInput) (*alerts.AlertsPolicy, error)
	updatePolicyFn            func(int, string, alerts.AlertsPolicyUpdateInput) (*alerts.AlertsPolicy, error)
	deletePolicyFn            func(int, string) error
	apiKey                    string
}

func (r *AlertPolicyReconciler) searchNrqlConditions(accountID int, searchParams alerts.NrqlConditionsSearchCriteria) ([]*alerts.NrqlAlertCondition, error) {
	if r.searchNrqlConditionsFn != nil {
		return r.searchNrqlConditionsFn(accountID, searchParams)
	}

	return r.Alerts.Alerts().SearchNrqlConditionsQuery(accountID, searchParams)
}

func (r *AlertPolicyReconciler) createStaticCondition(accountID int, policyID string, createInput alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error) {
	if r.createStaticConditionFn != nil {
		return r.createStaticConditionFn(accountID, policyID, createInput)
	}

	return r.Alerts.Alerts().CreateNrqlConditionStaticMutation(accountID, policyID, createInput)
}

func (r *AlertPolicyReconciler) createBaselineCondition(accountID int, policyID string, createInput alerts.NrqlConditionCreateInput) (*alerts.NrqlAlertCondition, error) {
	if r.createBaselineConditionFn != nil {
		return r.createBaselineConditionFn(accountID, policyID, createInput)
	}

	return r.Alerts.Alerts().CreateNrqlConditionBaselineMutation(accountID, policyID, createInput)
}

func (r *AlertPolicyReconciler) updateStaticCondition(accountID int, conditionID string, updateInput alerts.NrqlConditionUpdateInput) (*alerts.NrqlAlertCondition, error) {
	if r.updateStaticConditionFn != nil {
		return r.updateStaticConditionFn(accountID, conditionID, updateInput)
	}

	return r.Alerts.Alerts().UpdateNrqlConditionStaticMutation(accountID, conditionID, updateInput)
}

func (r *AlertPolicyReconciler) updateBaselineCondition(accountID int, conditionID string, updateInput alerts.NrqlConditionUpdateInput) (*alerts.NrqlAlertCondition, error) {
	if r.updateBaselineConditionFn != nil {
		return r.updateBaselineConditionFn(accountID, conditionID, updateInput)
	}

	return r.Alerts.Alerts().UpdateNrqlConditionBaselineMutation(accountID, conditionID, updateInput)
}

func (r *AlertPolicyReconciler) deleteConditionByID(accountID int, conditionID string) error {
	if r.deleteConditionFn != nil {
		return r.deleteConditionFn(accountID, conditionID)
	}

	_, err := r.Alerts.Alerts().DeleteConditionMutation(accountID, conditionID)
	return err
}

func (r *AlertPolicyReconciler) queryPolicyByID(accountID int, policyID string) (*alerts.AlertsPolicy, error) {
	if r.queryPolicyFn != nil {
		return r.queryPolicyFn(accountID, policyID)
	}

	return r.Alerts.Alerts().QueryPolicy(accountID, policyID)
}

func (r *AlertPolicyReconciler) searchPoliciesInAccount(accountID int, criteria alerts.AlertsPoliciesSearchCriteriaInput) ([]*alerts.AlertsPolicy, error) {
	if r.searchPoliciesFn != nil {
		return r.searchPoliciesFn(accountID, criteria)
	}

	return r.Alerts.Alerts().QueryPolicySearch(accountID, criteria)
}

func (r *AlertPolicyReconciler) createPolicyMutation(accountID int, input alerts.AlertsPolicyInput) (*alerts.AlertsPolicy, error) {
	if r.createPolicyFn != nil {
		return r.createPolicyFn(accountID, input)
	}

	return r.Alerts.Alerts().CreatePolicyMutation(accountID, input)
}

func (r *AlertPolicyReconciler) updatePolicyMutation(accountID int, policyID string, input alerts.AlertsPolicyUpdateInput) (*alerts.AlertsPolicy, error) {
	if r.updatePolicyFn != nil {
		return r.updatePolicyFn(accountID, policyID, input)
	}

	return r.Alerts.Alerts().UpdatePolicyMutation(accountID, policyID, input)
}

func (r *AlertPolicyReconciler) deletePolicyMutation(accountID int, policyID string) error {
	if r.deletePolicyFn != nil {
		return r.deletePolicyFn(accountID, policyID)
	}

	_, err := r.Alerts.Alerts().DeletePolicyMutation(accountID, policyID)
	return err
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AlertPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *AlertPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// Fetch AlertPolicy instance
	var policy alertsv1.AlertPolicy
	err := r.Get(ctx, req.NamespacedName, &policy)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("Policy 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET policy", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	r.Log.Info("Starting policy reconcile")

	// get API key
	r.apiKey, err = r.getAPIKeyOrSecret(policy)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	// init client
	alertClient, errAlertClient := r.AlertClient(r.apiKey, policy.Spec.Region)
	if errAlertClient != nil {
		r.Log.Error(errAlertClient, "Failed to create Alert Client")
		return ctrl.Result{}, errAlertClient
	}
	r.Alerts = alertClient

	// Handle policy deletion
	deleteFinalizer := "newrelic.policies.finalizer"

	// Examine DeletionTimestamp to determine if object is under deletion
	if policy.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&policy, deleteFinalizer) {
			controllerutil.AddFinalizer(&policy, deleteFinalizer)
			if err := r.Update(ctx, &policy); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// Policy is being deleted
		if controllerutil.ContainsFinalizer(&policy, deleteFinalizer) {
			// our finalizer is present, so handle external dependency
			if err := r.deletePolicy(&policy); err != nil {
				// if fail to delete the external dependency here, return with error so it can be retried
				return ctrl.Result{}, err
			}

			// remove finalizer from the list and update it
			controllerutil.RemoveFinalizer(&policy, deleteFinalizer)
			if err := r.Update(ctx, &policy); err != nil {
				return ctrl.Result{}, err
			}
		}

		// stop reconciliation as item is being deleted
		return ctrl.Result{}, nil
	}

	if r.matchesAppliedPolicyState(&policy) {
		r.Log.Info("No updates to policy. skipping reconcile")

		// Backfill the Ready condition on policies reconciled before it existed, and clear a stale
		// non-Ready condition once the spec is back in line with the applied state.
		if !meta.IsStatusConditionTrue(policy.Status.Conditions, alertsv1.AlertPolicyConditionReady) {
			setPolicyReady(&policy, metav1.ConditionTrue, alertsv1.AlertPolicyReasonSynced, "Policy is in sync with New Relic")
			if err := r.updateStatusIfChanged(ctx, &policy); err != nil {
				r.Log.Error(err, "failed to update AlertPolicy status")
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	outcome, syncErr := r.syncRemotePolicy(&policy)

	// Status is written even when sync failed so partial progress (policy ID, applied conditions)
	// and blocked reasons are persisted.
	if policy.Status.AppliedSpec == nil {
		// appliedSpec is required by the CRD schema, so a blocked first reconcile still needs a value.
		policy.Status.AppliedSpec = &alertsv1.AlertPolicySpec{}
	}
	r.Log.Info("Updating status...")
	if statusErr := r.updateStatusIfChanged(ctx, &policy); statusErr != nil {
		r.Log.Error(statusErr, "failed to update AlertPolicy status")
		if syncErr == nil {
			return ctrl.Result{}, statusErr
		}
	}

	if syncErr != nil {
		r.Log.Error(syncErr, "failed to reconcile New Relic alert policy")
		return ctrl.Result{}, syncErr
	}

	if outcome == syncBlocked {
		r.Log.Info("Policy reconcile blocked, will retry", "requeueAfter", blockedRequeueInterval.String())
		return ctrl.Result{RequeueAfter: blockedRequeueInterval}, nil
	}

	return ctrl.Result{}, nil
}

// syncRemotePolicy brings the New Relic policy in line with the spec. The remote policy is located
// by Status.PolicyID in Spec.AccountID, and by exact name in that account only when no ID is recorded.
// It makes no Kubernetes API calls; status changes are left on the policy for the caller to persist.
func (r *AlertPolicyReconciler) syncRemotePolicy(policy *alertsv1.AlertPolicy) (syncOutcome, error) {
	accountID := policy.Spec.AccountID

	if policy.Status.PolicyID != 0 && policy.Status.AppliedSpec != nil &&
		policy.Status.AppliedSpec.AccountID > 0 && policy.Status.AppliedSpec.AccountID != accountID {
		setPolicyReady(policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountIDChanged,
			fmt.Sprintf("spec.accountId is %d but policy %d was created in account %d; accountId cannot be changed. "+
				"Restore spec.accountId: %d, or delete and recreate the AlertPolicy to move it",
				accountID, policy.Status.PolicyID, policy.Status.AppliedSpec.AccountID, policy.Status.AppliedSpec.AccountID))
		return syncBlocked, nil
	}

	var remote *alerts.AlertsPolicy
	recreate := false

	if policy.Status.PolicyID != 0 {
		found, err := r.queryPolicyByID(accountID, strconv.Itoa(policy.Status.PolicyID))
		switch {
		case err == nil && found != nil && found.ID != "":
			remote = found
		case err == nil:
			// An empty result without a typed NotFound is not proof the policy is gone, so retry
			// with backoff rather than recreating it or resetting the recorded ID.
			return syncDone, fmt.Errorf("policy %d lookup in account %d returned no policy and no error, will retry",
				policy.Status.PolicyID, accountID)
		case isPolicyNotFound(err):
			// Deleted remotely, or not a policy of this account (for example, adopted from another
			// account by an earlier version). Start over with a new policy in the right account.
			r.Log.Info("Policy not found by ID in account, it will be created",
				"policyId", policy.Status.PolicyID, "accountId", accountID)
			policy.Status.PolicyID = 0
			for i := range policy.Spec.Conditions {
				policy.Spec.Conditions[i].ID = ""
			}
			recreate = true
		case isAccountAccessError(err):
			r.Log.V(1).Info("Account access error from policy lookup", "error", err.Error())
			setPolicyReady(policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountAccessDenied,
				fmt.Sprintf("API key cannot access account %d: %v", accountID, err))
			return syncBlocked, nil
		default:
			return syncDone, err
		}
	}

	if remote == nil && !recreate {
		matches, err := r.findPolicyByName(accountID, policy.Spec.Name)
		if err != nil {
			if isAccountAccessError(err) {
				r.Log.V(1).Info("Account access error from policy search", "error", err.Error())
				setPolicyReady(policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountAccessDenied,
					fmt.Sprintf("API key cannot access account %d: %v", accountID, err))
				return syncBlocked, nil
			}
			return syncDone, err
		}

		switch len(matches) {
		case 0:
		case 1:
			remote = matches[0]
		default:
			ids := make([]string, 0, len(matches))
			for _, m := range matches {
				ids = append(ids, m.ID)
			}
			setPolicyReady(policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAmbiguousPolicyName,
				fmt.Sprintf("%d policies named %q exist in account %d (IDs: %s); rename or delete the extras so the name is unique",
					len(matches), policy.Spec.Name, accountID, strings.Join(ids, ", ")))
			return syncBlocked, nil
		}
	}

	if remote != nil && remote.AccountID != 0 && remote.AccountID != accountID {
		setPolicyReady(policy, metav1.ConditionFalse, alertsv1.AlertPolicyReasonAccountMismatch,
			fmt.Sprintf("policy %s belongs to account %d, not spec.accountId %d", remote.ID, remote.AccountID, accountID))
		return syncBlocked, nil
	}

	if remote == nil {
		r.Log.Info("Creating policy", "policy", policy.Spec.Name, "accountId", accountID)
		if err := r.createPolicy(policy); err != nil {
			r.Log.Error(err, "failed to create New Relic alert policy")
			return syncDone, err
		}
	} else {
		remoteID, err := strconv.Atoi(remote.ID)
		if err != nil {
			return syncDone, fmt.Errorf("failed to convert policy ID %q to int: %w", remote.ID, err)
		}

		r.Log.Info("Matched on existing policy",
			"policyId", remote.ID,
			"policyName", remote.Name,
			"incidentPreference", string(remote.IncidentPreference),
		)
		policy.Status.PolicyID = remoteID
		policy.Status.AppliedSpec = buildAppliedPolicySpec(policy, remote)

		if r.hasPolicyChanged(remote, policy) {
			r.Log.Info("Updating existing policy", "policy", policy.Spec.Name)
			if err := r.updatePolicy(remote.ID, policy); err != nil {
				r.Log.Error(err, "failed to update New Relic alert policy")
				return syncDone, err
			}
			r.Log.Info("Policy updated successfully")
		} else {
			r.Log.Info("Policy unchanged, no update required")
			if err := r.reconcileConditionsIfNeeded(policy); err != nil {
				r.Log.Error(err, "failed to reconcile AlertPolicy conditions")
				return syncDone, err
			}
		}
	}

	setPolicyReady(policy, metav1.ConditionTrue, alertsv1.AlertPolicyReasonSynced, "Policy is in sync with New Relic")
	return syncDone, nil
}

// reconcileConditionsIfNeeded creates, updates and deletes the policy's nrql conditions when the spec or remote state differ.
func (r *AlertPolicyReconciler) reconcileConditionsIfNeeded(policy *alertsv1.AlertPolicy) error {
	if !shouldReconcileConditions(policy) {
		return nil
	}

	r.Log.Info("Checking if conditions need to be updated...")
	needsConditionReconcile, err := r.needsConditionReconcile(policy)
	if err != nil {
		return err
	}

	if !needsConditionReconcile {
		r.Log.Info("Condition spec and remote state match - no condition updates to apply.")
		return nil
	}

	r.Log.Info("Condition spec or remote state mismatch - updating conditions...")
	return r.handleConditions(policy)
}

// createPolicy creates a new policy
func (r *AlertPolicyReconciler) createPolicy(policy *alertsv1.AlertPolicy) error {
	alertPolicy := alerts.AlertsPolicyInput{
		IncidentPreference: toAlertsIncidentPreference(policy.Spec.IncidentPreference),
		Name:               policy.Spec.Name,
	}
	createdPolicy, err := r.createPolicyMutation(policy.Spec.AccountID, alertPolicy)
	if err != nil {
		r.Log.Error(err, "failed to create policy with inputs",
			"incidentPreference", policy.Spec.IncidentPreference,
			"policyId", policy.Status.PolicyID, // returns 0 if failed
			"region", policy.Spec.Region,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("Successfully created New Relic alert policy", "policyID", createdPolicy.ID)
	createdPolicyId, convertErr := strconv.Atoi(createdPolicy.ID)
	if convertErr != nil {
		r.Log.Error(convertErr, "failed to convert policyId to type int", "policyID", createdPolicy.ID)
		return fmt.Errorf("failed to convert created policy ID %q to int: %w", createdPolicy.ID, convertErr)
	}

	policy.Status.PolicyID = createdPolicyId
	// A new policy has no applied conditions yet, so drop any carried over from a previous remote policy.
	policy.Status.AppliedSpec = nil
	policy.Status.AppliedSpec = buildAppliedPolicySpec(policy, createdPolicy)

	if policy.Spec.Conditions != nil {
		policy.Status.AppliedSpec.Conditions = make([]alertsv1.PolicyCondition, len(policy.Spec.Conditions))
		copy(policy.Status.AppliedSpec.Conditions, policy.Spec.Conditions)
		if len(policy.Spec.Conditions) > 0 {
			if err := r.createPolicyConditions(policy); err != nil {
				return err
			}
		}
	}

	return nil
}

// createPolicyConditions creates every condition in policy.Spec.Conditions, attempting all of
// them even if some fail, and returns the first error encountered (if any) so the caller can
// surface it to Reconcile for a requeue with backoff instead of silently leaving the policy
// with zero or partial conditions.
func (r *AlertPolicyReconciler) createPolicyConditions(policy *alertsv1.AlertPolicy) error {
	r.Log.Info("Conditions specified for new policy. creating conditions...")
	var allErrors []error
	for i, polCondition := range policy.Spec.Conditions {
		if createdCondition, err := r.createCondition(&polCondition, policy); err != nil {
			r.Log.Error(err, "failed to create condition",
				"conditionName", polCondition.Name)
			allErrors = append(allErrors, fmt.Errorf("condition %q: %w", polCondition.Name, err))
		} else {
			policy.Spec.Conditions[i] = *createdCondition
			policy.Status.AppliedSpec.Conditions[i] = *createdCondition
		}
	}

	if len(allErrors) > 0 {
		return allErrors[0]
	}

	return nil
}

// updatePolicy updates an existing policy
func (r *AlertPolicyReconciler) updatePolicy(policyID string, policy *alertsv1.AlertPolicy) error {
	updateInput := alerts.AlertsPolicyUpdateInput{
		Name:               policy.Spec.Name,
		IncidentPreference: toAlertsIncidentPreference(policy.Spec.IncidentPreference),
	}

	updatedPolicy, err := r.updatePolicyMutation(policy.Spec.AccountID, policyID, updateInput)
	if err != nil {
		r.Log.Error(err, "failed to update New Relic alert policy",
			"policyId", policy.Status.PolicyID,
			"region", policy.Spec.Region,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("Successfully updated New Relic alert policy", "policyID", updatedPolicy.ID)

	updatedPolicyID, convertErr := strconv.Atoi(updatedPolicy.ID)
	if convertErr != nil {
		r.Log.Error(convertErr, "failed to convert policyId to type int", "policyID", updatedPolicy.ID)
		return fmt.Errorf("failed to convert updated policy ID %q to int: %w", updatedPolicy.ID, convertErr)
	}

	// Refresh the applied snapshot (keeping applied conditions) so a rename or preference change
	// is not seen as a pending change on the next reconcile.
	policy.Status.PolicyID = updatedPolicyID
	policy.Status.AppliedSpec = buildAppliedPolicySpec(policy, updatedPolicy)

	return r.reconcileConditionsIfNeeded(policy)
}

// deletePolicy deletes the policy recorded in status (Status.PolicyID), in the account it was created
// in. It never searches by name at delete time. Note that on first reconcile with no recorded ID, a
// single exact-name match in the account is adopted (syncRemotePolicy), so deleting the CR afterwards
// deletes that policy by ID.
func (r *AlertPolicyReconciler) deletePolicy(policy *alertsv1.AlertPolicy) error {
	if policy.Status.PolicyID == 0 {
		r.Log.Info("No policy ID recorded in status, nothing to delete in New Relic", "policy", policy.Spec.Name)
		return nil
	}

	accountID := policyAccountForDelete(policy)
	policyID := strconv.Itoa(policy.Status.PolicyID)
	r.Log.Info("Deleting policy", "policy", policy.Spec.Name, "policyId", policy.Status.PolicyID, "accountId", accountID)
	deleteErr := r.deletePolicyMutation(accountID, policyID)
	if deleteErr == nil {
		return nil
	}

	if isPolicyNotFound(deleteErr) {
		r.Log.Info("Policy already deleted in New Relic", "policyId", policy.Status.PolicyID)
		return nil
	}

	// The delete mutation returns untyped errors that cannot be classified by message, so confirm
	// with a lookup by ID that the policy is really gone before letting the finalizer go.
	if _, queryErr := r.queryPolicyByID(accountID, policyID); isPolicyNotFound(queryErr) {
		r.Log.Info("Policy already deleted in New Relic", "policyId", policy.Status.PolicyID, "deleteError", deleteErr.Error())
		return nil
	}

	r.Log.Error(deleteErr, "Error deleting New Relic policy",
		"policyId", policy.Status.PolicyID,
		"policyName", policy.Spec.Name,
	)
	return deleteErr
}

// policyAccountForDelete returns the account a policy was created in, falling back to the spec for
// policies that have no applied account recorded.
func policyAccountForDelete(policy *alertsv1.AlertPolicy) int {
	if policy.Status.AppliedSpec != nil && policy.Status.AppliedSpec.AccountID > 0 {
		return policy.Status.AppliedSpec.AccountID
	}

	return policy.Spec.AccountID
}

// findPolicyByName returns the policies in the account whose name equals name exactly.
// NerdGraph policy search cannot filter by name (and the client drops the criteria), so this lists
// the account's policies with empty criteria and matches client-side. It does not change any status.
func (r *AlertPolicyReconciler) findPolicyByName(accountID int, name string) ([]*alerts.AlertsPolicy, error) {
	policies, err := r.searchPoliciesInAccount(accountID, alerts.AlertsPoliciesSearchCriteriaInput{})
	if err != nil {
		return nil, err
	}

	var matches []*alerts.AlertsPolicy
	for _, candidate := range policies {
		if candidate == nil || candidate.Name != name {
			continue
		}

		if candidate.AccountID != 0 && candidate.AccountID != accountID {
			continue
		}

		matches = append(matches, candidate)
	}

	return matches, nil
}

// isPolicyNotFound reports whether err is a typed NotFound from the New Relic client. Untyped errors
// (for example, GraphQL errors from the mutations) are never classified by message, because a
// message match could mistake an unrelated failure for a missing policy.
func isPolicyNotFound(err error) bool {
	var notFound *nrerrors.NotFound
	return stderrors.As(err, &notFound)
}

// isAccountAccessError reports whether err means the API key cannot access the account.
func isAccountAccessError(err error) bool {
	if err == nil {
		return false
	}

	var unauthorized *nrerrors.UnauthorizedError
	if stderrors.As(err, &unauthorized) {
		return true
	}

	msg := strings.ToLower(err.Error())
	for _, fragment := range accountAccessErrorSubstrings {
		if strings.Contains(msg, fragment) {
			return true
		}
	}

	return false
}

// toAlertsIncidentPreference maps the spec value to the NerdGraph enum, defaulting to PER_CONDITION.
func toAlertsIncidentPreference(preference string) alerts.AlertsIncidentPreference {
	switch preference {
	case "PER_POLICY":
		return alerts.AlertsIncidentPreferenceTypes.PER_POLICY
	case "PER_CONDITION":
		return alerts.AlertsIncidentPreferenceTypes.PER_CONDITION
	case "PER_CONDITION_AND_TARGET":
		return alerts.AlertsIncidentPreferenceTypes.PER_CONDITION_AND_TARGET
	default:
		return alerts.AlertsIncidentPreferenceTypes.PER_CONDITION
	}
}

// setPolicyReady records the Ready condition on the policy status.
func setPolicyReady(policy *alertsv1.AlertPolicy, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:               alertsv1.AlertPolicyConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: policy.Generation,
	})
}

func snapshotAlertPolicySpec(spec alertsv1.AlertPolicySpec) *alertsv1.AlertPolicySpec {
	cloned := spec.DeepCopy()
	if cloned == nil {
		return &alertsv1.AlertPolicySpec{}
	}

	return cloned
}

func buildAppliedPolicySpec(policy *alertsv1.AlertPolicy, existingPolicy *alerts.AlertsPolicy) *alertsv1.AlertPolicySpec {
	appliedSpec := &alertsv1.AlertPolicySpec{}
	if policy.Status.AppliedSpec != nil && policy.Status.AppliedSpec.Conditions != nil {
		appliedSpec.Conditions = append([]alertsv1.PolicyCondition(nil), policy.Status.AppliedSpec.Conditions...)
	}

	appliedSpec.Name = existingPolicy.Name
	appliedSpec.IncidentPreference = string(existingPolicy.IncidentPreference)
	appliedSpec.Region = policy.Spec.Region
	appliedSpec.AccountID = policy.Spec.AccountID
	appliedSpec.APIKey = policy.Spec.APIKey
	appliedSpec.APIKeySecret = policy.Spec.APIKeySecret

	return appliedSpec
}

func findAppliedConditionIndex(conditions []alertsv1.PolicyCondition, conditionName string) int {
	for index, condition := range conditions {
		if conditionIdentity(condition) == conditionName {
			return index
		}
	}

	return -1
}

func conditionIdentity(condition alertsv1.PolicyCondition) string {
	if condition.Name != "" {
		return condition.Name
	}

	return condition.Spec.Name
}

func shouldReconcileConditions(policy *alertsv1.AlertPolicy) bool {
	if len(policy.Spec.Conditions) > 0 {
		return true
	}

	return policy.Status.AppliedSpec != nil && len(policy.Status.AppliedSpec.Conditions) > 0
}

func (r *AlertPolicyReconciler) needsConditionReconcile(policy *alertsv1.AlertPolicy) (bool, error) {
	if r.haveConditionsChanged(policy) {
		return true, nil
	}

	if len(policy.Spec.Conditions) == 0 {
		return false, nil
	}

	remoteConditionsByName, err := r.getExistingNrqlConditionsByName(policy)
	if err != nil {
		return false, err
	}

	for _, configuredCondition := range policy.Spec.Conditions {
		conditionName := conditionIdentity(configuredCondition)
		remoteCondition, ok := remoteConditionsByName[conditionName]
		if !ok || !r.matchesRemoteConditionSpec(remoteCondition, &configuredCondition) {
			return true, nil
		}
	}

	return false, nil
}

func (r *AlertPolicyReconciler) matchesAppliedPolicyState(policy *alertsv1.AlertPolicy) bool {
	if policy.Status.PolicyID == 0 || policy.Status.AppliedSpec == nil {
		return false
	}

	appliedSpec := policy.Status.AppliedSpec
	if policy.Spec.Name != appliedSpec.Name ||
		policy.Spec.IncidentPreference != appliedSpec.IncidentPreference ||
		policy.Spec.Region != appliedSpec.Region ||
		policy.Spec.AccountID != appliedSpec.AccountID ||
		policy.Spec.APIKey != appliedSpec.APIKey ||
		policy.Spec.APIKeySecret != appliedSpec.APIKeySecret {
		return false
	}

	return !r.haveConditionsChanged(policy)
}

// haveConditionsChanged validates if condition spec and appliedStatus differ
func (r *AlertPolicyReconciler) haveConditionsChanged(policy *alertsv1.AlertPolicy) bool {
	configuredConditions := policy.Spec.Conditions
	observedConditions := []alertsv1.PolicyCondition{}
	if policy.Status.AppliedSpec != nil {
		observedConditions = policy.Status.AppliedSpec.Conditions
	}

	if len(configuredConditions) != len(observedConditions) {
		return true
	}

	for i := range configuredConditions {
		if !reflect.DeepEqual(configuredConditions[i].Spec, observedConditions[i].Spec) {
			return true
		}
	}

	return false
}

// hasPolicyChanged checks if the remote policy and policy spec differ. The caller guarantees the
// remote policy is the one recorded in status, so only the mutable fields are compared.
func (r *AlertPolicyReconciler) hasPolicyChanged(existingPolicy *alerts.AlertsPolicy, newPolicy *alertsv1.AlertPolicy) bool {
	return existingPolicy.Name != newPolicy.Spec.Name ||
		string(existingPolicy.IncidentPreference) != newPolicy.Spec.IncidentPreference
}

// This holds details for conditions eligible to delete (no longer exist)
type processedAlertConditions struct {
	accountId int //nolint:golint
	id        string
	name      string
	processed bool
}

// handleConditions manages the creation, update, and delete of nrql conditions.
// It attempts every condition, so a single condition's persistent failure
// doesn't prevent the status snapshot below from recording the conditions that DID apply successfully,
// otherwise those conditions would never be found in AppliedSpec on the next reconcile
// and would be redundantly re-updated on every pass. The first error encountered (if any)
// is returned so the caller can still surface/requeue it.
func (r *AlertPolicyReconciler) handleConditions(policy *alertsv1.AlertPolicy) error {
	observedConditions := []alertsv1.PolicyCondition{}
	if policy.Status.AppliedSpec != nil {
		observedConditions = policy.Status.AppliedSpec.Conditions
	}

	// build struct of existing conditions to mark as processed/eligible for deletion (if not processed)
	processedConditions := []processedAlertConditions{}
	for _, existing := range observedConditions {
		processedConditions = append(processedConditions, processedAlertConditions{
			accountId: policy.Spec.AccountID,
			id:        existing.ID,
			name:      conditionIdentity(existing),
			processed: false,
		})
	}

	var allErrors []error

	for i, configuredCondition := range policy.Spec.Conditions {
		r.Log.Info("Processing condition", "conditionName", configuredCondition.Spec.Name)
		existingCondition, err := r.getExistingNrqlCondition(&configuredCondition, policy)
		if err != nil {
			r.Log.Error(err, "failed to fetch existing condition")
			allErrors = append(allErrors, fmt.Errorf("condition %q: %w", configuredCondition.Spec.Name, err))
			continue
		}

		if existingCondition != nil { // if condition exists in NR already by name
			configuredCondition.Name = existingCondition.Name
			configuredCondition.ID = existingCondition.ID

			observedIndex := findAppliedConditionIndex(observedConditions, configuredCondition.Spec.Name)
			if observedIndex >= 0 {
				processedConditions[observedIndex].processed = true

				if !reflect.DeepEqual(configuredCondition.Spec, observedConditions[observedIndex].Spec) { // compare spec/status state to determine if config mismatch
					r.Log.Info("Configuration difference detected. Updating condition to current spec specified.")
					updatedCondition, err := r.updateCondition(&configuredCondition, policy)
					if err != nil {
						allErrors = append(allErrors, fmt.Errorf("condition %q: %w", configuredCondition.Spec.Name, err))
						continue
					}
					policy.Spec.Conditions[i] = *updatedCondition // apply latest configuration to spec
				} else {
					policy.Spec.Conditions[i] = configuredCondition
				}
				continue
			}

			if r.matchesRemoteConditionSpec(existingCondition, &configuredCondition) {
				r.Log.Info("Condition exists in New Relic and already matches desired spec. Restoring applied status.")
				policy.Spec.Conditions[i] = configuredCondition
				continue
			}

			r.Log.Info("Condition exists in New Relic but not in applied status. Reconciling condition to current spec.")
			updatedCondition, err := r.updateCondition(&configuredCondition, policy)
			if err != nil {
				allErrors = append(allErrors, fmt.Errorf("condition %q: %w", configuredCondition.Spec.Name, err))
				continue
			}
			policy.Spec.Conditions[i] = *updatedCondition
			continue
		} else { // configured condition name does not exist in NR already, create it
			createdCondition, err := r.createCondition(&configuredCondition, policy)
			if err != nil {
				allErrors = append(allErrors, fmt.Errorf("condition %q: %w", configuredCondition.Spec.Name, err))
				continue
			}

			observedIndex := findAppliedConditionIndex(observedConditions, configuredCondition.Spec.Name)
			if observedIndex >= 0 {
				processedConditions[observedIndex].processed = true
			}

			configuredCondition.Name = createdCondition.Name
			configuredCondition.ID = createdCondition.ID
			policy.Spec.Conditions[i] = *createdCondition
		}
	}

	r.Log.Info("Checking if any conditions need to be deleted")
	if len(processedConditions) > 0 {
		remoteConditionsByName, err := r.getExistingNrqlConditionsByName(policy)
		if err != nil {
			allErrors = append(allErrors, err)
		} else {
			for _, pc := range processedConditions {
				if !pc.processed {
					if pc.id == "" {
						if existingCondition, ok := remoteConditionsByName[pc.name]; ok {
							pc.id = existingCondition.ID
						}
					}

					r.Log.Info("deleting condition", "condName", pc.name)
					if err := r.deleteCondition(pc); err != nil {
						allErrors = append(allErrors, fmt.Errorf("condition %q: %w", pc.name, err))
						continue
					}
				}
			}
		}
	}

	policy.Status.AppliedSpec = snapshotAlertPolicySpec(policy.Spec) // apply latest spec to status

	if len(allErrors) > 0 {
		return allErrors[0]
	}

	return nil
}

func (r *AlertPolicyReconciler) getExistingNrqlConditionsByName(policy *alertsv1.AlertPolicy) (map[string]*alerts.NrqlAlertCondition, error) {
	polString := fmt.Sprint(policy.Status.PolicyID)
	searchParams := alerts.NrqlConditionsSearchCriteria{
		PolicyID: polString,
	}

	existingConditions, err := r.searchNrqlConditions(policy.Spec.AccountID, searchParams)
	if err != nil {
		r.Log.Error(err, "failed to fetch conditions",
			"policyID", polString,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return nil, err
	}

	conditionsByName := make(map[string]*alerts.NrqlAlertCondition, len(existingConditions))
	for _, existingCondition := range existingConditions {
		if existingCondition == nil || existingCondition.Name == "" {
			continue
		}

		conditionsByName[existingCondition.Name] = existingCondition
	}

	return conditionsByName, nil
}

func (r *AlertPolicyReconciler) matchesRemoteConditionSpec(existingCondition *alerts.NrqlAlertCondition, desiredCondition *alertsv1.PolicyCondition) bool {
	if existingCondition == nil {
		return false
	}

	return reflect.DeepEqual(desiredCondition.Spec, buildPolicyConditionSpecFromRemote(existingCondition))
}

func buildPolicyConditionSpecFromRemote(existingCondition *alerts.NrqlAlertCondition) alertsv1.PolicyConditionSpec {
	remoteSpec := alertsv1.PolicyConditionSpec{
		NrqlConditionSpec: alertsv1.NrqlConditionSpec{
			AlertsNrqlBaseSpec: alertsv1.AlertsNrqlBaseSpec{
				Description: existingCondition.Description,
				Enabled:     existingCondition.Enabled,
				Name:        existingCondition.Name,
				Nrql: alertsv1.NrqlConditionCreateQuery{
					Query:            existingCondition.Nrql.Query,
					DataAccountID:    existingCondition.Nrql.DataAccountId,
					EvaluationOffset: existingCondition.Nrql.EvaluationOffset,
				},
				RunbookURL:                existingCondition.RunbookURL,
				Type:                      existingCondition.Type,
				ViolationTimeLimit:        existingCondition.ViolationTimeLimit,
				ViolationTimeLimitSeconds: existingCondition.ViolationTimeLimitSeconds,
				TitleTemplate:             existingCondition.TitleTemplate,
			},
			AlertsBaselineSpecificSpec: alertsv1.AlertsBaselineSpecificSpec{
				BaselineDirection: existingCondition.BaselineDirection,
			},
		},
	}

	if existingCondition.Expiration != nil {
		remoteSpec.Expiration = &alertsv1.AlertsNrqlConditionExpiration{
			ExpirationDuration:          existingCondition.Expiration.ExpirationDuration,
			CloseViolationsOnExpiration: existingCondition.Expiration.CloseViolationsOnExpiration,
			OpenViolationOnExpiration:   existingCondition.Expiration.OpenViolationOnExpiration,
			IgnoreOnExpectedTermination: existingCondition.Expiration.IgnoreOnExpectedTermination,
		}
	}

	if existingCondition.Signal != nil {
		remoteSpec.Signal = &alertsv1.AlertsNrqlConditionSignal{
			AggregationWindow: existingCondition.Signal.AggregationWindow,
			EvaluationOffset:  existingCondition.Signal.EvaluationOffset,
			EvaluationDelay:   existingCondition.Signal.EvaluationDelay,
			FillOption:        existingCondition.Signal.FillOption,
			FillValue:         existingCondition.Signal.FillValue,
			AggregationMethod: existingCondition.Signal.AggregationMethod,
			AggregationDelay:  existingCondition.Signal.AggregationDelay,
			AggregationTimer:  existingCondition.Signal.AggregationTimer,
			SlideBy:           existingCondition.Signal.SlideBy,
		}
	}

	for _, term := range existingCondition.Terms {
		threshold := ""
		if term.Threshold != nil {
			threshold = strconv.FormatFloat(*term.Threshold, 'f', -1, 64)
		}

		remoteSpec.Terms = append(remoteSpec.Terms, alertsv1.AlertsNrqlConditionTerm{
			Operator:                     term.Operator,
			Priority:                     term.Priority,
			Threshold:                    threshold,
			ThresholdDuration:            term.ThresholdDuration,
			ThresholdOccurrences:         term.ThresholdOccurrences,
			DisableHealthStatusReporting: term.DisableHealthStatusReporting,
		})
	}

	return remoteSpec
}

func (r *AlertPolicyReconciler) updateStatusIfChanged(ctx context.Context, policy *alertsv1.AlertPolicy) error {
	key := types.NamespacedName{Namespace: policy.Namespace, Name: policy.Name}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current alertsv1.AlertPolicy
		if err := r.Get(ctx, key, &current); err != nil {
			return err
		}

		if reflect.DeepEqual(current.Status, policy.Status) {
			return nil
		}

		current.Status = policy.Status
		return r.Status().Update(ctx, &current)
	})
}

// createCondition creates a new condition under an existing policy
func (r *AlertPolicyReconciler) createCondition(condition *alertsv1.PolicyCondition, policy *alertsv1.AlertPolicy) (*alertsv1.PolicyCondition, error) {
	r.Log.Info("Creating condition",
		"conditionName", condition.Spec.Name,
	)
	createInput := r.fetchNrqlCreateInput(condition)

	var createdCondition *alerts.NrqlAlertCondition
	var err error
	polString := fmt.Sprint(policy.Status.PolicyID)

	if condition.Spec.Type == "STATIC" { // golint:nolint
		createdCondition, err = r.createStaticCondition(policy.Spec.AccountID, polString, createInput)
	} else { // baseline
		if condition.Spec.BaselineDirection != nil {
			createdCondition, err = r.createBaselineCondition(policy.Spec.AccountID, polString, createInput)
		}
	}

	if err != nil {
		r.Log.Error(err, "failed to create nrql condition",
			"policyId", polString,
			"conditionName", condition.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
			"validationErrors", nrqlConditionValidationErrors(err),
		)
		return nil, err
	}

	r.Log.Info("createdCondition", "condition", createdCondition)

	condition.Name = createdCondition.Name
	condition.ID = createdCondition.ID

	return condition, nil
}

// updateCondition updates an existing condition under the existing policy
func (r *AlertPolicyReconciler) updateCondition(condition *alertsv1.PolicyCondition, policy *alertsv1.AlertPolicy) (*alertsv1.PolicyCondition, error) {
	r.Log.Info("Updating condition",
		"conditionName", condition.Spec.Name,
		"conditionId", condition.ID,
	)
	updateInput := r.fetchNrqlUpdateInput(condition)

	var updatedCondition *alerts.NrqlAlertCondition
	var err error

	if condition.Spec.Type == "STATIC" { // golint:nolint
		updatedCondition, err = r.updateStaticCondition(policy.Spec.AccountID, condition.ID, updateInput)
	} else { // baseline
		if condition.Spec.BaselineDirection != nil {
			updatedCondition, err = r.updateBaselineCondition(policy.Spec.AccountID, condition.ID, updateInput)
		} else {
			r.Log.Info("Missing baseline direction configuration, review config")
		}
	}

	if err != nil {
		r.Log.Error(err, "failed to update nrql condition",
			"conditionId", condition.ID,
			"conditionName", condition.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
			"validationErrors", nrqlConditionValidationErrors(err),
		)
		return condition, err
	}

	r.Log.Info("updatedCondition", "condition", updatedCondition)

	condition.ID = updatedCondition.ID
	condition.Name = updatedCondition.Name

	return condition, nil
}

// nrqlConditionValidationErrors extracts field-level validation detail from a NerdGraph
// error response, if the underlying error carries any. Returns nil if err doesn't wrap a
// *alerts.GraphQLErrorResponse, or none of its errors carry ValidationErrors.
func nrqlConditionValidationErrors(err error) []string {
	var gqlErr *alerts.GraphQLErrorResponse
	if !stderrors.As(err, &gqlErr) {
		return nil
	}

	var details []string
	for _, e := range gqlErr.Errors {
		for _, ve := range e.Extensions.ValidationErrors {
			details = append(details, fmt.Sprintf("%s: %s", ve.Name, ve.Reason))
		}
	}

	return details
}

// deleteCondition deletes a single condition
func (r *AlertPolicyReconciler) deleteCondition(condition processedAlertConditions) error {
	r.Log.Info("Deleting condition", "condition", condition)
	err := r.deleteConditionByID(condition.accountId, condition.id)
	if err != nil {
		return err
	}

	return nil
}

// getExistingCondition fetches existing nrql conditions in a given policy and validates if configured condition matches existing condition by name
// name changes assume that new conditions are to be created
func (r *AlertPolicyReconciler) getExistingNrqlCondition(currentCondition *alertsv1.PolicyCondition, policy *alertsv1.AlertPolicy) (*alerts.NrqlAlertCondition, error) {
	r.Log.Info("Checking for existing condition", "conditionName", currentCondition.Spec.Name)

	polString := fmt.Sprint(policy.Status.PolicyID)

	searchParams := alerts.NrqlConditionsSearchCriteria{
		PolicyID: polString,
	}

	existingConditions, err := r.searchNrqlConditions(policy.Spec.AccountID, searchParams)
	if err != nil {
		r.Log.Error(err, "failed to fetch condition",
			"policyID", currentCondition.Spec.ExistingPolicyID,
			"conditionName", currentCondition.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return nil, err
	}

	var existingCond *alerts.NrqlAlertCondition
	if len(existingConditions) > 0 {
		for _, existingCondition := range existingConditions {
			// check if existing condition matches configured condition by name
			if existingCondition.Name == currentCondition.Spec.Name {
				existingCond = existingCondition
				currentCondition.Name = existingCondition.Name
				currentCondition.ID = existingCondition.ID
				break
			}
		}

		return existingCond, nil
	}

	return nil, nil
}

// fetchNrqlUpdateInput returns formatted spec for creating nrql conditions
func (r *AlertPolicyReconciler) fetchNrqlUpdateInput(cond *alertsv1.PolicyCondition) alerts.NrqlConditionUpdateInput {
	conditionInput := alerts.NrqlConditionUpdateInput{}
	conditionInput.Description = cond.Spec.Description
	conditionInput.Enabled = cond.Spec.Enabled
	conditionInput.Name = cond.Spec.Name

	conditionInput.Nrql = alerts.NrqlConditionUpdateQuery{}
	conditionInput.Nrql.Query = cond.Spec.Nrql.Query
	conditionInput.Nrql.DataAccountId = cond.Spec.Nrql.DataAccountID
	conditionInput.Nrql.EvaluationOffset = cond.Spec.Nrql.EvaluationOffset

	conditionInput.RunbookURL = cond.Spec.RunbookURL
	conditionInput.ViolationTimeLimit = cond.Spec.ViolationTimeLimit
	conditionInput.ViolationTimeLimitSeconds = cond.Spec.ViolationTimeLimitSeconds
	conditionInput.BaselineDirection = cond.Spec.BaselineDirection

	if cond.Spec.Expiration != nil {
		conditionInput.Expiration = &alerts.AlertsNrqlConditionExpiration{}
		conditionInput.Expiration.ExpirationDuration = cond.Spec.Expiration.ExpirationDuration
		conditionInput.Expiration.CloseViolationsOnExpiration = cond.Spec.Expiration.CloseViolationsOnExpiration
		conditionInput.Expiration.OpenViolationOnExpiration = cond.Spec.Expiration.OpenViolationOnExpiration
		conditionInput.Expiration.IgnoreOnExpectedTermination = cond.Spec.Expiration.IgnoreOnExpectedTermination
	}

	if cond.Spec.Signal != nil {
		conditionInput.Signal = &alerts.AlertsNrqlConditionUpdateSignal{}
		conditionInput.Signal.AggregationWindow = cond.Spec.Signal.AggregationWindow
		conditionInput.Signal.EvaluationOffset = cond.Spec.Signal.EvaluationOffset
		conditionInput.Signal.EvaluationDelay = cond.Spec.Signal.EvaluationDelay
		conditionInput.Signal.FillOption = cond.Spec.Signal.FillOption
		conditionInput.Signal.FillValue = cond.Spec.Signal.FillValue

		conditionInput.Signal.AggregationMethod = cond.Spec.Signal.AggregationMethod
		conditionInput.Signal.AggregationDelay = cond.Spec.Signal.AggregationDelay
		conditionInput.Signal.AggregationTimer = cond.Spec.Signal.AggregationTimer
		conditionInput.Signal.SlideBy = cond.Spec.Signal.SlideBy
	}

	for _, term := range cond.Spec.Terms {
		t := alerts.NrqlConditionTerm{}

		t.Operator = term.Operator
		t.Priority = term.Priority

		// When parsing a string
		f, err := strconv.ParseFloat(term.Threshold, 64)
		if err != nil {
			r.Log.Error(err, "failed to parse term threshold", "threshold", term.Threshold)
		}

		t.Threshold = &f
		t.ThresholdDuration = term.ThresholdDuration
		t.ThresholdOccurrences = term.ThresholdOccurrences
		t.DisableHealthStatusReporting = term.DisableHealthStatusReporting

		conditionInput.Terms = append(conditionInput.Terms, t)
	}

	conditionInput.TitleTemplate = cond.Spec.TitleTemplate

	return conditionInput
}

// fetchNrqlCreateInput returns formatted spec for creating nrql conditions
func (r *AlertPolicyReconciler) fetchNrqlCreateInput(cond *alertsv1.PolicyCondition) alerts.NrqlConditionCreateInput {
	conditionInput := alerts.NrqlConditionCreateInput{}
	conditionInput.Description = cond.Spec.Description
	conditionInput.Enabled = cond.Spec.Enabled
	conditionInput.Name = cond.Spec.Name

	conditionInput.Nrql = alerts.NrqlConditionCreateQuery{}
	conditionInput.Nrql.Query = cond.Spec.Nrql.Query
	conditionInput.Nrql.DataAccountId = cond.Spec.Nrql.DataAccountID
	conditionInput.Nrql.EvaluationOffset = cond.Spec.Nrql.EvaluationOffset

	conditionInput.RunbookURL = cond.Spec.RunbookURL
	conditionInput.ViolationTimeLimit = cond.Spec.ViolationTimeLimit
	conditionInput.ViolationTimeLimitSeconds = cond.Spec.ViolationTimeLimitSeconds
	conditionInput.BaselineDirection = cond.Spec.BaselineDirection

	if cond.Spec.Expiration != nil {
		conditionInput.Expiration = &alerts.AlertsNrqlConditionExpiration{}
		conditionInput.Expiration.ExpirationDuration = cond.Spec.Expiration.ExpirationDuration
		conditionInput.Expiration.CloseViolationsOnExpiration = cond.Spec.Expiration.CloseViolationsOnExpiration
		conditionInput.Expiration.OpenViolationOnExpiration = cond.Spec.Expiration.OpenViolationOnExpiration
		conditionInput.Expiration.IgnoreOnExpectedTermination = cond.Spec.Expiration.IgnoreOnExpectedTermination
	}

	if cond.Spec.Signal != nil {
		conditionInput.Signal = &alerts.AlertsNrqlConditionCreateSignal{}
		conditionInput.Signal.AggregationWindow = cond.Spec.Signal.AggregationWindow
		conditionInput.Signal.EvaluationOffset = cond.Spec.Signal.EvaluationOffset
		conditionInput.Signal.EvaluationDelay = cond.Spec.Signal.EvaluationDelay
		conditionInput.Signal.FillOption = cond.Spec.Signal.FillOption
		conditionInput.Signal.FillValue = cond.Spec.Signal.FillValue

		conditionInput.Signal.AggregationMethod = cond.Spec.Signal.AggregationMethod
		conditionInput.Signal.AggregationDelay = cond.Spec.Signal.AggregationDelay
		conditionInput.Signal.AggregationTimer = cond.Spec.Signal.AggregationTimer
		conditionInput.Signal.SlideBy = cond.Spec.Signal.SlideBy
	}

	for _, term := range cond.Spec.Terms {
		t := alerts.NrqlConditionTerm{}

		t.Operator = term.Operator
		t.Priority = term.Priority

		// When parsing a string
		f, err := strconv.ParseFloat(term.Threshold, 64)
		if err != nil {
			r.Log.Error(err, "failed to parse term threshold", "threshold", term.Threshold)
		}

		t.Threshold = &f
		t.ThresholdDuration = term.ThresholdDuration
		t.ThresholdOccurrences = term.ThresholdOccurrences
		t.DisableHealthStatusReporting = term.DisableHealthStatusReporting

		conditionInput.Terms = append(conditionInput.Terms, t)
	}

	conditionInput.TitleTemplate = cond.Spec.TitleTemplate

	return conditionInput
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *AlertPolicyReconciler) getAPIKeyOrSecret(policy alertsv1.AlertPolicy) (string, error) {
	if policy.Spec.APIKey != "" {
		return policy.Spec.APIKey, nil
	}

	if policy.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: policy.Spec.APIKeySecret.Namespace, Name: policy.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[policy.Spec.APIKeySecret.KeyName])) // in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}

	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AlertPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.AlertPolicy{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
