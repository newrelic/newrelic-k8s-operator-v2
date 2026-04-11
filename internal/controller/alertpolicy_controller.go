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
	"fmt"
	"reflect"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
	"github.com/newrelic/newrelic-k8s-operator-v2/interfaces"
)

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

	//Fetch AlertPolicy instance
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

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(policy)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	//init client
	alertClient, errAlertClient := r.AlertClient(r.apiKey, policy.Spec.Region)
	if errAlertClient != nil {
		r.Log.Error(errAlertClient, "Failed to create Alert Client")
		return ctrl.Result{}, errAlertClient
	}
	r.Alerts = alertClient

	//Handle policy deletion
	deleteFinalizer := "newrelic.policies.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if policy.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&policy, deleteFinalizer) {
			controllerutil.AddFinalizer(&policy, deleteFinalizer)
			if err := r.Update(ctx, &policy); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//Policy is being deleted
		if controllerutil.ContainsFinalizer(&policy, deleteFinalizer) {
			//our finalizer is present, so handle external dependency
			if err := r.deletePolicy(&policy); err != nil {
				//if fail to delete the external dependency here, return with error so it can be retried
				return ctrl.Result{}, err
			}

			//remove finalizer from the list and update it
			controllerutil.RemoveFinalizer(&policy, deleteFinalizer)
			if err := r.Update(ctx, &policy); err != nil {
				return ctrl.Result{}, err
			}
		}

		//stop reconciliation as item is being deleted
		return ctrl.Result{}, nil
	}

	if r.matchesAppliedPolicyState(&policy) {
		r.Log.Info("No updates to policy. skipping reconcile")
		return ctrl.Result{}, nil
	}

	//Check if policy already exists by name
	existingPolicy, err := r.getExistingPolicyByName(&policy)
	if err != nil {
		r.Log.Error(err, "Failed to fetch existing policyId")
		return ctrl.Result{}, err
	}

	if existingPolicy != nil {
		if existingPolicy.ID == policy.Status.PolicyID && policy.Status.PolicyID != 0 {
			//if policy exists, check if the spec has changed
			if r.hasPolicyChanged(existingPolicy, &policy) {
				//Update the policy only if spec has changed
				r.Log.Info("Updating existing policy", "policy", policy.Spec.Name)
				err = r.updatePolicy(existingPolicy.ID, &policy)
				if err != nil {
					r.Log.Error(err, "failed to update New Relic alert policy")
					return ctrl.Result{}, err
				}
				r.Log.Info("Policy updated successfully")

				//Update Policy Status
				r.Log.Info("Updating status...")
				err = r.updateStatusIfChanged(ctx, &policy)
				if err != nil {
					r.Log.Error(err, "failed to update AlertPolicy status")
					return ctrl.Result{}, err
				}

			} else {
				r.Log.Info("Policy unchanged, no update required")
				if shouldReconcileConditions(&policy) {
					r.Log.Info("Checking if conditions need to be updated...")

					needsConditionReconcile, err := r.needsConditionReconcile(&policy)
					if err != nil {
						r.Log.Error(err, "failed to evaluate AlertPolicy conditions")
						return ctrl.Result{}, err
					}

					if !needsConditionReconcile {
						r.Log.Info("Condition spec and remote state match - no condition updates to apply.")
						return ctrl.Result{}, nil
					}

					r.Log.Info("Condition spec or remote state mismatch - updating conditions...")
					err = r.handleConditions(&policy)
					if err != nil {
						r.Log.Error(err, "failed to reconcile AlertPolicy conditions")
						return ctrl.Result{}, err
					}

					//Update Policy Status
					r.Log.Info("Updating status...")
					err = r.updateStatusIfChanged(ctx, &policy)
					if err != nil {
						r.Log.Error(err, "failed to update AlertPolicy status")
						return ctrl.Result{}, err
					}
				}
			}
		} else {
			//If policy doesnt exist AND the existing id does not match that of the current id, create it
			err := r.createPolicy(&policy)
			if err != nil {
				r.Log.Error(err, "failed to create New Relic alert policy")
				return ctrl.Result{}, err
			}

			//Update Policy Status
			r.Log.Info("Updating status...")
			err = r.updateStatusIfChanged(ctx, &policy)
			if err != nil {
				r.Log.Error(err, "failed to update AlertPolicy status")
				return ctrl.Result{}, err
			}
		}
	} else {
		//new policy name doesn't exist - check if configured name matches previous configured name - if no, update policy with new configured name
		appliedPolicyName := ""
		if policy.Status.AppliedSpec != nil {
			appliedPolicyName = policy.Status.AppliedSpec.Name
		}

		if policy.Status.PolicyID != 0 && appliedPolicyName != policy.Spec.Name {
			r.Log.Info("Updating previously configured policy", "policy", appliedPolicyName)
			err = r.updatePolicy(policy.Status.PolicyID, &policy)
			if err != nil {
				r.Log.Error(err, "failed to update New Relic alert policy")
				return ctrl.Result{}, err
			}
			r.Log.Info("Policy updated successfully")

			//Update Policy Status
			r.Log.Info("Updating status...")
			err = r.updateStatusIfChanged(ctx, &policy)
			if err != nil {
				r.Log.Error(err, "failed to update AlertPolicy status")
				return ctrl.Result{}, err
			}

		} else {
			err := r.createPolicy(&policy)
			if err != nil {
				r.Log.Error(err, "failed to create New Relic alert policy")
				return ctrl.Result{}, err
			}

			//Update Policy Status
			r.Log.Info("Updating status...")
			err = r.updateStatusIfChanged(ctx, &policy)
			if err != nil {
				r.Log.Error(err, "failed to update AlertPolicy status")
				return ctrl.Result{}, err
			}

		}
	}

	return ctrl.Result{}, nil
}

// createPolicy creates a new policy
func (r *AlertPolicyReconciler) createPolicy(policy *alertsv1.AlertPolicy) error {
	var incPref alerts.AlertsIncidentPreference
	switch policy.Spec.IncidentPreference {
	case "PER_POLICY":
		incPref = alerts.AlertsIncidentPreferenceTypes.PER_POLICY
	case "PER_CONDITION":
		incPref = alerts.AlertsIncidentPreferenceTypes.PER_CONDITION
	case "PER_CONDITION_AND_TARGET":
		incPref = alerts.AlertsIncidentPreferenceTypes.PER_CONDITION_AND_TARGET
	default:
		incPref = alerts.AlertsIncidentPreferenceTypes.PER_CONDITION
	}

	alertPolicy := alerts.AlertsPolicyInput{
		IncidentPreference: incPref,
		Name:               policy.Spec.Name,
	}
	createdPolicy, err := r.Alerts.Alerts().CreatePolicyMutation(policy.Spec.AccountID, alertPolicy)
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
		r.Log.Error(err, "failed to convert policyId to type int")
		return err
	}

	policy.Status.PolicyID = createdPolicyId
	policy.Status.AppliedSpec = &alertsv1.AlertPolicySpec{}
	policy.Status.AppliedSpec.Name = createdPolicy.Name
	policy.Status.AppliedSpec.IncidentPreference = string(createdPolicy.IncidentPreference)
	policy.Status.AppliedSpec.Region = policy.Spec.Region
	policy.Status.AppliedSpec.AccountID = policy.Spec.AccountID
	policy.Status.AppliedSpec.APIKey = policy.Spec.APIKey
	policy.Status.AppliedSpec.APIKeySecret = policy.Spec.APIKeySecret

	if policy.Spec.Conditions != nil {
		policy.Status.AppliedSpec.Conditions = policy.Spec.Conditions
		if len(policy.Spec.Conditions) > 0 {
			r.Log.Info("Conditions specified for new policy. creating conditions...")
			for i, polCondition := range policy.Spec.Conditions {
				if createdCondition, err := r.createCondition(&polCondition, policy); err != nil {
					r.Log.Error(err, "failed to create condition",
						"conditionName", polCondition.Name)
				} else {
					policy.Spec.Conditions[i] = *createdCondition
					policy.Status.AppliedSpec.Conditions[i] = *createdCondition
				}
			}
		}
	}

	// policy.Status.AppliedSpec = &policy.Spec

	return nil
}

// updatePolicy updates an existing policy
func (r *AlertPolicyReconciler) updatePolicy(policyID int, policy *alertsv1.AlertPolicy) error {
	updateParams := alerts.Policy{
		ID:                 policyID,
		Name:               policy.Spec.Name,
		IncidentPreference: alerts.IncidentPreferenceType(policy.Spec.IncidentPreference),
	}

	updatedPolicy, err := r.Alerts.Alerts().UpdatePolicy(updateParams)
	if err != nil {
		r.Log.Error(err, "failed to update New Relic alert policy",
			"policyId", policy.Status.PolicyID,
			"region", policy.Spec.Region,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("Successfully updated New Relic alert policy", "policyID", updatedPolicy.ID)

	policy.Status.PolicyID = updatedPolicy.ID

	if shouldReconcileConditions(policy) {
		r.Log.Info("Checking if conditions need to be updated...")

		needsConditionReconcile, err := r.needsConditionReconcile(policy)
		if err != nil {
			return err
		}

		if !needsConditionReconcile {
			r.Log.Info("Conditions unchanged - skipping handle")
			return nil
		}

		err = r.handleConditions(policy)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *AlertPolicyReconciler) deletePolicy(policy *alertsv1.AlertPolicy) error {
	existingPolicy, err := r.getExistingPolicyByName(policy)
	if err != nil {
		r.Log.Error(err, "Failed to fetch existing policyId to delete.")
		return err
	}

	if existingPolicy != nil {
		r.Log.Info("Deleting policy", "policy", existingPolicy.Name)
		_, err := r.Alerts.Alerts().DeletePolicy(existingPolicy.ID)
		if err != nil {
			r.Log.Error(err, "Error deleting New Relic policy",
				"policyId", policy.Status.PolicyID,
				"policyName", policy.Spec.Name,
			)
			return err
		}
	} else {
		r.Log.Info("Failed to fetch policy to delete", "policy", policy.Spec.Name)
	}

	return nil
}

// checkForExistingPolicy fetches a list of policies by name and validates if spec matches existing
func (r *AlertPolicyReconciler) getExistingPolicyByName(policy *alertsv1.AlertPolicy) (*alerts.Policy, error) {

	//TODO: switch to GraphQL once PR merged here: https://github.com/newrelic/newrelic-client-go/pull/1285
	// listParams := alerts.AlertsPoliciesSearchCriteriaInput{name: Policy.Spec.Name}

	// existingPolicies, err := r.Alerts.Alerts().QueryPolicySearch(policy.Spec.AccountID, testParams)
	// if err != nil {
	// 	r.Log.Info("newPolicyError", "err", err)
	// }

	listParams := alerts.ListPoliciesParams{
		Name: policy.Spec.Name,
	}
	existingPolicies, err := r.Alerts.Alerts().ListPolicies(&listParams)
	if err != nil {
		return nil, err
	}

	var existingPol *alerts.Policy
	if len(existingPolicies) > 0 {
		for _, existingPolicy := range existingPolicies {
			if existingPolicy.Name == policy.Spec.Name {
				r.Log.Info("Matched on existing policy",
					"policyId", existingPolicy.ID,
					"policyName", existingPolicy.Name,
					"incidentPreference", string(existingPolicy.IncidentPreference),
				)
				existingPol = &existingPolicy
				break
			}
		}

		if existingPol == nil {
			return nil, nil
		}

		policy.Status.PolicyID = existingPol.ID
		policy.Status.AppliedSpec = buildAppliedPolicySpec(policy, existingPol)

		return existingPol, nil
	}

	return nil, nil
}

func snapshotAlertPolicySpec(spec alertsv1.AlertPolicySpec) *alertsv1.AlertPolicySpec {
	cloned := spec.DeepCopy()
	if cloned == nil {
		return &alertsv1.AlertPolicySpec{}
	}

	return cloned
}

func buildAppliedPolicySpec(policy *alertsv1.AlertPolicy, existingPolicy *alerts.Policy) *alertsv1.AlertPolicySpec {
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

// hasPolicyChanged checks if existing policy and policy spec differ
func (r *AlertPolicyReconciler) hasPolicyChanged(existingPolicy *alerts.Policy, newPolicy *alertsv1.AlertPolicy) bool {
	return existingPolicy.ID == newPolicy.Status.PolicyID && (existingPolicy.Name != newPolicy.Spec.Name ||
		string(existingPolicy.IncidentPreference) != newPolicy.Spec.IncidentPreference)
}

// This holds details for conditions eligible to delete (no longer exist)
type processedAlertConditions struct {
	accountId int //nolint:golint
	id        string
	name      string
	processed bool
}

// handleConditions manages the creation, update, and delete of nrql conditions
func (r *AlertPolicyReconciler) handleConditions(policy *alertsv1.AlertPolicy) error {
	observedConditions := []alertsv1.PolicyCondition{}
	if policy.Status.AppliedSpec != nil {
		observedConditions = policy.Status.AppliedSpec.Conditions
	}

	//build struct of existing conditions to mark as processed/eligible for deletion (if not processed)
	processedConditions := []processedAlertConditions{}
	for _, existing := range observedConditions {
		processedConditions = append(processedConditions, processedAlertConditions{
			accountId: policy.Spec.AccountID,
			id:        existing.ID,
			name:      conditionIdentity(existing),
			processed: false,
		})
	}

	for i, configuredCondition := range policy.Spec.Conditions {
		r.Log.Info("Processing condition", "conditionName", configuredCondition.Spec.Name)
		existingCondition, err := r.getExistingNrqlCondition(&configuredCondition, policy)
		if err != nil {
			r.Log.Error(err, "failed to fetch existing condition")
			return err
		}

		if existingCondition != nil { // if condition exists in NR already by name
			configuredCondition.Name = existingCondition.Name
			configuredCondition.ID = existingCondition.ID

			observedIndex := findAppliedConditionIndex(observedConditions, configuredCondition.Spec.Name)
			if observedIndex >= 0 {
				processedConditions[observedIndex].processed = true

				if !reflect.DeepEqual(configuredCondition.Spec, observedConditions[observedIndex].Spec) { //compare spec/status state to determine if config mismatch
					r.Log.Info("Configuration difference detected. Updating condition to current spec specified.")
					updatedCondition, err := r.updateCondition(&configuredCondition, policy)
					if err != nil {
						return err
					}
					policy.Spec.Conditions[i] = *updatedCondition //apply latest configuration to spec
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
				return err
			}
			policy.Spec.Conditions[i] = *updatedCondition
			continue
		} else { //configured condition name does not exist in NR already, create it
			createdCondition, err := r.createCondition(&configuredCondition, policy)
			if err != nil {
				return err
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
			return err
		}

		for _, pc := range processedConditions {
			if !pc.processed {
				if pc.id == "" {
					if existingCondition, ok := remoteConditionsByName[pc.name]; ok {
						pc.id = existingCondition.ID
					}
				}

				r.Log.Info("deleting condition", "condName", pc.name)
				err := r.deleteCondition(pc)
				if err != nil {
					return err
				}
			}
		}
	}

	policy.Status.AppliedSpec = snapshotAlertPolicySpec(policy.Spec) //apply latest spec to status

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
			Operator:             term.Operator,
			Priority:             term.Priority,
			Threshold:            threshold,
			ThresholdDuration:    term.ThresholdDuration,
			ThresholdOccurrences: term.ThresholdOccurrences,
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

	if condition.Spec.Type == "STATIC" { //golint:nolint
		createdCondition, err = r.createStaticCondition(policy.Spec.AccountID, polString, createInput)
	} else { //baseline
		if condition.Spec.BaselineDirection != nil {
			createdCondition, err = r.createBaselineCondition(policy.Spec.AccountID, polString, createInput)
		}
	}

	if err != nil {
		r.Log.Error(err, "failed to create nrql condition",
			"policyId", polString,
			"conditionName", condition.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
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

	if condition.Spec.Type == "STATIC" { //golint:nolint
		updatedCondition, err = r.updateStaticCondition(policy.Spec.AccountID, condition.ID, updateInput)
	} else { //baseline
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
		)
		return condition, err
	}

	r.Log.Info("updatedCondition", "condition", updatedCondition)

	condition.ID = updatedCondition.ID
	condition.Name = updatedCondition.Name

	return condition, nil
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
			//check if existing condition matches configured condition by name
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
			//log.Error()
		}

		t.Threshold = &f
		t.ThresholdDuration = term.ThresholdDuration
		t.ThresholdOccurrences = term.ThresholdOccurrences

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
			//log.Error()
		}

		t.Threshold = &f
		t.ThresholdDuration = term.ThresholdDuration
		t.ThresholdOccurrences = term.ThresholdOccurrences

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

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[policy.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

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
