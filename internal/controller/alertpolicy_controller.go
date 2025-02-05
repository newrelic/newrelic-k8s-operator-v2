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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	alertsv1 "github.com/newrelic/newrelic-kubernetes-operator-v2/api/v1"
	"github.com/newrelic/newrelic-kubernetes-operator-v2/interfaces"
)

// AlertPolicyReconciler reconciles a AlertPolicy object
type AlertPolicyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	Alerts      interfaces.NewRelicClientInterface
	AlertClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey      string
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

	if reflect.DeepEqual(&policy.Spec, policy.Status.AppliedSpec) {
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
				err = r.Status().Update(ctx, &policy)
				if err != nil {
					r.Log.Error(err, "failed to update AlertPolicy status")
					return ctrl.Result{}, err
				}

			} else {
				r.Log.Info("Policy unchanged, no update required")
				if policy.Spec.Conditions != nil {
					if len(policy.Spec.Conditions) > 0 {
						r.Log.Info("Checking if conditions need to be updated...")

						if !r.haveConditionsChanged(&policy) {
							r.Log.Info("Condition spec and status match - validating conditions exist in New Relic")
							hasMismatch, err := r.conditionHasRemoteMismatch(&policy)
							if err != nil {
								return ctrl.Result{}, err
							}
							if !hasMismatch {
								r.Log.Info("All applied conditions have associated conditions in New Relic. No condition updates to apply.")
								return ctrl.Result{}, nil
							} else {
								r.Log.Info("One or more conditions were deleted in New Relic, but still exists in appliedStatus - attempting to recreate.")
							}
						} else {
							r.Log.Info("Condition spec and status mismatch - updating conditions...")
						}
						r.handleConditions(&policy)

						//Update Policy Status
						r.Log.Info("Updating status...")
						err = r.Status().Update(ctx, &policy)
						if err != nil {
							r.Log.Error(err, "failed to update AlertPolicy status")
							return ctrl.Result{}, err
						}
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
			err = r.Status().Update(ctx, &policy)
			if err != nil {
				r.Log.Error(err, "failed to update AlertPolicy status")
				return ctrl.Result{}, err
			}
		}
	} else {
		//new policy name doesn't exist - check if configured name matches previous configured name - if no, update policy with new configured name
		if policy.Status.PolicyID != 0 && policy.Status.AppliedSpec.Name != policy.Spec.Name {
			r.Log.Info("Updating previously configured policy", "policy", policy.Status.AppliedSpec.Name)
			err = r.updatePolicy(policy.Status.PolicyID, &policy)
			if err != nil {
				r.Log.Error(err, "failed to update New Relic alert policy")
				return ctrl.Result{}, err
			}
			r.Log.Info("Policy updated successfully")

			//Update Policy Status
			r.Log.Info("Updating status...")
			err = r.Status().Update(ctx, &policy)
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
			err = r.Status().Update(ctx, &policy)
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
	const (
		PerPolicy             alerts.IncidentPreferenceType = "PER_POLICY"
		PerConditon           alerts.IncidentPreferenceType = "PER_CONDITION"
		PerConditionAndTarget alerts.IncidentPreferenceType = "PER_CONDITION_AND_TARGET"
	)

	var incPref alerts.IncidentPreferenceType
	switch policy.Spec.IncidentPreference {
	case "PER_POLICY":
		incPref = PerPolicy
	case "PER_CONDITION":
		incPref = PerConditon
	case "PER_CONDITION_AND_TARGET":
		incPref = PerConditionAndTarget
	default:
		incPref = "PER_CONDITION"
	}

	alertPolicy := alerts.Policy{
		IncidentPreference: incPref,
		Name:               policy.Spec.Name,
	}
	createdPolicy, err := r.Alerts.Alerts().CreatePolicy(alertPolicy)
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

	policy.Status.PolicyID = createdPolicy.ID
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

	if policy.Spec.Conditions != nil {
		if len(policy.Spec.Conditions) > 0 {
			r.Log.Info("Checking if conditions need to be updated...")

			if !r.haveConditionsChanged(policy) {
				r.Log.Info("Conditions unchanged - skipping handle")
				return nil
			}

			r.handleConditions(policy)
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

		return existingPol, nil
	}

	return nil, nil
}

// haveConditionsChanged validates if condition spec and appliedStatus differ
func (r *AlertPolicyReconciler) haveConditionsChanged(policy *alertsv1.AlertPolicy) bool {
	configuredConditions := policy.Spec.Conditions
	observedConditions := policy.Status.AppliedSpec.Conditions

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

// conditionHasRemoteMismatch fetches conditions from existing policy and determines if condition in appliedSpec has been deleted in New Relic
func (r *AlertPolicyReconciler) conditionHasRemoteMismatch(policy *alertsv1.AlertPolicy) (bool, error) {
	observedConditions := policy.Status.AppliedSpec.Conditions

	for _, observedCondition := range observedConditions {
		existingCondition, err := r.getExistingNrqlCondition(&observedCondition, policy)
		if err != nil {
			r.Log.Error(err, "failed to fetch existing condition")
			return false, err
		}

		if existingCondition == nil { // condition deleted on the NR side, so return true to path into handleConditions to recreate it
			return true, nil
		} //TODO: add logic to compare retrieved condition spec against appliedStatus spec
	}

	return false, nil
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

	//build struct of existing conditions to mark as processed/eligible for deletion (if not processed)
	processedConditions := []processedAlertConditions{}
	for _, existing := range policy.Status.AppliedSpec.Conditions {
		processedConditions = append(processedConditions, processedAlertConditions{
			accountId: policy.Spec.AccountID,
			id:        existing.ID,
			name:      existing.Name,
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
			for _, observedCondition := range policy.Status.AppliedSpec.Conditions {
				if observedCondition.Name == configuredCondition.Spec.Name { // if condition exists in both spec/current state
					configuredCondition.Name = existingCondition.Name
					configuredCondition.ID = existingCondition.ID
					processedConditions[i].processed = true

					if !reflect.DeepEqual(configuredCondition.Spec, observedCondition.Spec) { //compare spec/status state to determine if config mismatch
						r.Log.Info("Configuration difference detected. Updating condition to current spec specified.")
						updatedCondition, err := r.updateCondition(&configuredCondition, policy)
						if err != nil {
							return err
						}
						policy.Spec.Conditions[i] = *updatedCondition //apply latest configuration to spec
					}
				}
			}
		} else { //configured condition name does not exist in NR already, create it
			createdCondition, err := r.createCondition(&configuredCondition, policy)
			if err != nil {
				return err
			}
			configuredCondition.Name = createdCondition.Name
			configuredCondition.ID = createdCondition.ID
			policy.Spec.Conditions[i] = *createdCondition
		}
	}

	r.Log.Info("Checking if any conditions need to be deleted")
	if len(processedConditions) > 0 {
		for _, pc := range processedConditions {
			if !pc.processed {
				r.Log.Info("deleting condition", "condName", pc.name)
				err := r.deleteCondition(pc)
				if err != nil {
					return err
				}
			}
		}
	}

	policy.Status.AppliedSpec = &policy.Spec //apply latest spec to status

	return nil
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
		createdCondition, err = r.Alerts.Alerts().CreateNrqlConditionStaticMutation(policy.Spec.AccountID, polString, createInput)
	} else { //baseline
		if condition.Spec.BaselineDirection != nil {
			createdCondition, err = r.Alerts.Alerts().CreateNrqlConditionBaselineMutation(policy.Spec.AccountID, polString, createInput)
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
		updatedCondition, err = r.Alerts.Alerts().UpdateNrqlConditionStaticMutation(policy.Spec.AccountID, condition.ID, updateInput)
	} else { //baseline
		if condition.Spec.BaselineDirection != nil {
			updatedCondition, err = r.Alerts.Alerts().UpdateNrqlConditionBaselineMutation(policy.Spec.AccountID, condition.ID, updateInput)
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
	_, err := r.Alerts.Alerts().DeleteConditionMutation(condition.accountId, condition.id)
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

	existingConditions, err := r.Alerts.Alerts().SearchNrqlConditionsQuery(policy.Spec.AccountID, searchParams)
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
