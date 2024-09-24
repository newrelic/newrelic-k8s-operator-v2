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
	"reflect"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	alertsv1 "github.com/newrelic/newrelic-kubernetes-operator-v2/api/v1"
	"github.com/newrelic/newrelic-kubernetes-operator-v2/interfaces"
)

// NrqlConditionReconciler reconciles a NrqlCondition object
type NrqlConditionReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	Alerts      interfaces.NewRelicClientInterface
	AlertClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey      string
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=nrqlconditions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=nrqlconditions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=nrqlconditions/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NrqlCondition object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *NrqlConditionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	//Fetch NrqlCondition instance
	var condition alertsv1.NrqlCondition
	err := r.Get(ctx, req.NamespacedName, &condition)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("Condition 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET nrql condition", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	r.Log.Info("Starting condition reconcile")

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(condition)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	//init client
	alertClient, errAlertClient := r.AlertClient(r.apiKey, condition.Spec.Region)
	if errAlertClient != nil {
		r.Log.Error(errAlertClient, "Failed to create Alert Client")
		return ctrl.Result{}, errAlertClient
	}
	r.Alerts = alertClient

	//Handle condition deletion
	deleteFinalizer := "newrelic.nrqlcondition.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if condition.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&condition, deleteFinalizer) {
			controllerutil.AddFinalizer(&condition, deleteFinalizer)
			if err := r.Update(ctx, &condition); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//Condition is being deleted
		if controllerutil.ContainsFinalizer(&condition, deleteFinalizer) {
			if condition.Status.ConditionID == "" {
				r.Log.Info("No Condition ID set, remove finalizer")
				controllerutil.RemoveFinalizer(&condition, deleteFinalizer)
				if err := r.Update(ctx, &condition); err != nil {
					return ctrl.Result{}, err
				}
			}
			//our finalizer is present, so handle external dependency
			if err := r.deleteNrqlCondition(&condition); err != nil {
				r.Log.Error(err, "failed to delete nrql condition",
					"conditionId", condition.Status.ConditionID,
					"conditionName", condition.Spec.Name,
					"apiKey", interfaces.PartialAPIKey(r.apiKey),
				)

				if err.Error() == "resource not found" {
					r.Log.Info("resource not found, deleting nrql condition resource")
				} else {
					//if fail to delete the external dependency here, return with error so it can be retried
					return ctrl.Result{}, err
				}
			}

			//remove finalizer from the list and update it
			controllerutil.RemoveFinalizer(&condition, deleteFinalizer)
			if err := r.Update(ctx, &condition); err != nil {
				r.Log.Error(err, "failed to update condition after deleting New Relic nrql condition")
				return ctrl.Result{}, err
			}
		}

		//stop reconciliation as item is being deleted
		r.Log.Info("Successfully deleted nrql condition", "conditionName", condition.Spec.Name)
		return ctrl.Result{}, nil
	}

	//If no spec changes, skip reconcile
	if reflect.DeepEqual(&condition.Spec, condition.Status.AppliedSpec) {
		r.Log.Info("skipping reconcile")
		return ctrl.Result{}, nil
	}

	//Look for existing condition
	existingCondition, err := r.getExistingCondition(&condition)
	if err != nil {
		r.Log.Error(err, "failed to fetch existing condition")
		return ctrl.Result{}, err
	}

	//Create or update condition
	if existingCondition != nil && condition.Status.ConditionID != "" { //exists, and no previous state
		r.Log.Info("Condition found and previous condition id state exists, checking for config mismatch",
			"fetchedConditionId", existingCondition.ID,
			"currentCondiitionId", condition.Status.ConditionID,
		)
		if !reflect.DeepEqual(existingCondition, &condition.Spec) || !reflect.DeepEqual(&condition.Spec, condition.Status.AppliedSpec) {
			r.Log.Info("Configuration mismatch found, updating condition", "conditionId", condition.Status.ConditionID)
			err := r.updateNrqlCondition(&condition)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Log.Info("condition updated successfully", "conditionId", condition.Status.ConditionID)
		} else {
			r.Log.Info("condition unchanged, no update required")
			return ctrl.Result{}, err
		}
	} else { //doesnt exist
		if condition.Status.ConditionID != "" && !reflect.DeepEqual(&condition.Spec, condition.Status.AppliedSpec) {
			r.Log.Info("updating existing condition", "conditionName", condition.Spec.Name)
			err := r.updateNrqlCondition(&condition)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Log.Info("condition updated successfully", "conditionId", condition.Status.ConditionID)
		} else {
			r.Log.Info("No condition found and no conditionId state set, creating new condition", "conditionName", condition.Spec.Name)
			err := r.createNrqlCondition(&condition)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Log.Info("condition created successfully", "conditionId", condition.Status.ConditionID)
			err = r.Status().Update(ctx, &condition)
			if err != nil {
				r.Log.Error(err, "failed to update NrqlCondition status")
				return ctrl.Result{}, err
			}
		}
	}

	//update status
	err = r.Status().Update(ctx, &condition)
	if err != nil {
		r.Log.Error(err, "failed to update NrqlCondition status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NrqlConditionReconciler) getExistingCondition(condition *alertsv1.NrqlCondition) (*alerts.NrqlAlertCondition, error) {
	r.Log.Info("Checking for existing condition", "conditionId", condition.Status.ConditionID)

	searchParams := alerts.NrqlConditionsSearchCriteria{
		PolicyID: condition.Spec.ExistingPolicyID,
	}
	existingConditions, err := r.Alerts.Alerts().SearchNrqlConditionsQuery(condition.Spec.AccountID, searchParams)
	if err != nil {
		r.Log.Error(err, "failed to get list of nrql conditions",
			"policyID", condition.Spec.ExistingPolicyID,
			"conditionID", condition.Status.ConditionID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return nil, err
	}

	var existingCond *alerts.NrqlAlertCondition
	if len(existingConditions) > 0 {
		for _, existingCondition := range existingConditions {
			if existingCondition.ID == condition.Status.ConditionID {
				r.Log.Info("Found condition, returning",
					"conditionId", existingCondition.ID,
					"conditionName", existingCondition.Name,
				)
				existingCond = existingCondition
				condition.Status.ConditionID = existingCondition.ID
				break
			}
		}

		condition.Status.AppliedSpec = &condition.Spec

		return existingCond, nil
	}

	return nil, nil
}

// createNrqlCondition creates a single nrql condition
func (r *NrqlConditionReconciler) createNrqlCondition(condition *alertsv1.NrqlCondition) error {
	createInput := r.translateNrqlCreateInput(condition)

	var createdCondition *alerts.NrqlAlertCondition
	var err error

	if condition.Spec.Type == "STATIC" { //static
		createdCondition, err = r.Alerts.Alerts().CreateNrqlConditionStaticMutation(condition.Spec.AccountID, condition.Spec.ExistingPolicyID, createInput)
	} else { //baseline
		if condition.Spec.BaselineDirection != nil {
			createdCondition, err = r.Alerts.Alerts().CreateNrqlConditionBaselineMutation(condition.Spec.AccountID, condition.Spec.ExistingPolicyID, createInput)
		}
	}

	if err != nil {
		r.Log.Error(err, "failed to create nrql condition",
			"conditionId", condition.Status.ConditionID,
			"conditionName", condition.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}
	condition.Status.AppliedSpec = &condition.Spec
	condition.Status.ConditionID = createdCondition.ID

	return nil
}

// updateNrqlCondition updates a single nrql condition
func (r *NrqlConditionReconciler) updateNrqlCondition(condition *alertsv1.NrqlCondition) error {
	updateInput := r.translateNrqlUpdateInput(condition)

	var updatedCondition *alerts.NrqlAlertCondition
	var err error

	if condition.Spec.Type == "STATIC" { //static
		updatedCondition, err = r.Alerts.Alerts().UpdateNrqlConditionStaticMutation(condition.Spec.AccountID, condition.Status.ConditionID, updateInput)
	} else { //baseline
		if condition.Spec.BaselineDirection != nil {
			updatedCondition, err = r.Alerts.Alerts().UpdateNrqlConditionBaselineMutation(condition.Spec.AccountID, condition.Status.ConditionID, updateInput)
		} else {
			r.Log.Info("Missing baseline direction configuration, review config")
		}
	}

	if err != nil {
		r.Log.Error(err, "failed to update nrql condition",
			"conditionId", condition.Status.ConditionID,
			"conditionName", condition.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}
	condition.Status.AppliedSpec = &condition.Spec
	condition.Status.ConditionID = updatedCondition.ID

	return nil
}

// deleteNrqlCondition deletes a single nrql condition
func (r *NrqlConditionReconciler) deleteNrqlCondition(condition *alertsv1.NrqlCondition) error {
	r.Log.Info("Deleting condition", "conditionId", condition.Status.ConditionID)
	_, err := r.Alerts.Alerts().DeleteConditionMutation(condition.Spec.AccountID, condition.Status.ConditionID)
	if err != nil {
		return err
	}

	return nil
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *NrqlConditionReconciler) getAPIKeyOrSecret(condition alertsv1.NrqlCondition) (string, error) {
	if condition.Spec.APIKey != "" {
		return condition.Spec.APIKey, nil
	}

	if condition.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: condition.Spec.APIKeySecret.Namespace, Name: condition.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[condition.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}

	return "", nil
}

// translateNrqlUpdateInput returns formatted spec for creating nrql conditions
func (r *NrqlConditionReconciler) translateNrqlUpdateInput(cond *alertsv1.NrqlCondition) alerts.NrqlConditionUpdateInput {
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

// translateNrqlCreateInput returns formatted spec for creating nrql conditions
func (r *NrqlConditionReconciler) translateNrqlCreateInput(cond *alertsv1.NrqlCondition) alerts.NrqlConditionCreateInput {
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

// SetupWithManager sets up the controller with the Manager.
func (r *NrqlConditionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.NrqlCondition{}).
		Complete(r)
}
