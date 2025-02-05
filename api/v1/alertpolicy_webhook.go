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

package v1

import (
	"strconv"
	"strings"

	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var Log = logf.Log.WithName("alertpolicy-resource")
var defaultPolicyIncidentPreference = "PER_CONDITION"

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *AlertPolicy) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-alertpolicy,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertpolicies,verbs=create;update,versions=v1,name=malertpolicy.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &AlertPolicy{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *AlertPolicy) Default() {
	Log.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-alertpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertpolicies,verbs=create;update,versions=v1,name=valertpolicy.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &AlertPolicy{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertPolicy) ValidateCreate() (admission.Warnings, error) {
	Log.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidatePolicySchema()...)

	if r.Spec.Conditions != nil {
		if len(r.Spec.Conditions) > 0 {
			allErrs = append(allErrs, r.ValidateConditionSchema()...)
		} else {
			allErrs = append(allErrs, field.Required(field.NewPath("spec").Child("conditions"), "conditions must be defined and contain at least one element"))
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertPolicy"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertPolicy) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	Log.Info("validate update", "name", r.Name)

	var allErrs field.ErrorList
	var prevPolicy = old.(*AlertPolicy)

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidatePolicySchema()...)

	if r.Spec.Conditions != nil {
		if len(r.Spec.Conditions) > 0 {
			allErrs = append(allErrs, r.ValidateConditionSchema()...)
			allErrs = append(allErrs, r.ValidateTypeSwitch(prevPolicy)...)
		} else {
			allErrs = append(allErrs, field.Required(field.NewPath("spec").Child("conditions"), "conditions must be defined and contain at least one element"))
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertPolicy"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *AlertPolicy) ValidateDelete() (admission.Warnings, error) {
	Log.Info("validate delete", "name", r.Name)

	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *AlertPolicy) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}

	if r.Spec.IncidentPreference == "" {
		r.Spec.IncidentPreference = defaultPolicyIncidentPreference
	}
	r.Spec.IncidentPreference = strings.ToUpper(r.Spec.IncidentPreference)
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *AlertPolicy) ValidateAPIKeyOrSecret() field.ErrorList {
	var keyErrs field.ErrorList

	apiKeySet := r.Spec.APIKey != ""
	apiKeySecretSet := r.Spec.APIKeySecret != (NewRelicSecret{})

	if !(r.Spec.AccountID > 0) {
		keyErrs = append(keyErrs, field.Required(field.NewPath("spec").Child("accountId"), "accountId must be set as type integer"))
	}

	if !apiKeySet && !apiKeySecretSet {
		keyErrs = append(keyErrs, field.Required(field.NewPath("spec").Child("apiKey"), "either apiKey or apiKeySecret must be set"))
	} else if apiKeySet && apiKeySecretSet {
		keyErrs = append(keyErrs, field.Required(field.NewPath("spec").Child("apiKey"), "only one of apiKey or apiKeySecret can be set"))
	}

	if apiKeySecretSet {
		if r.Spec.APIKeySecret.Name == "" || r.Spec.APIKeySecret.Namespace == "" || r.Spec.APIKeySecret.KeyName == "" {
			keyErrs = append(keyErrs, field.Required(field.NewPath("spec").Child("apiKeySecret"), "missing required apiKeySecret field - validate inputs"))
		}
	}

	return keyErrs
}

// ValidatePolicySchema validates the required fields for a policy
func (r *AlertPolicy) ValidatePolicySchema() field.ErrorList {
	var schemaErrs field.ErrorList

	if r.Spec.Name == "" {
		schemaErrs = append(schemaErrs, field.Required(field.NewPath("spec").Child("name"), "policy name must be set"))
	}

	if r.Spec.IncidentPreference != "PER_POLICY" && r.Spec.IncidentPreference != "PER_CONDITION" && r.Spec.IncidentPreference != "PER_CONDITION_AND_TARGET" {
		schemaErrs = append(schemaErrs, field.Required(field.NewPath("spec").Child("incidentPreference"), "incidentPreference must be one of: PER_POLICY | PER_CONDITION | PER_CONDITION_AND_TARGET"))
	}

	return schemaErrs
}

// ValidateConditionSchema validates the required fields for conditions under a policy
func (r *AlertPolicy) ValidateConditionSchema() field.ErrorList {
	var condErrs field.ErrorList

	for i, condition := range r.Spec.Conditions {
		var signalSet = condition.Spec.Signal != nil

		if condition.Spec.Name == "" {
			condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("name"), "condition name must be set"))
		}
		if condition.Spec.Type != "BASELINE" && condition.Spec.Type != "STATIC" {
			condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("type"), "condition type must be one of: STATIC | BASELINE"))
		}
		if condition.Spec.Type == "BASELINE" {
			if condition.Spec.BaselineDirection != nil {
				if *condition.Spec.BaselineDirection != alerts.NrqlBaselineDirections.UpperOnly && *condition.Spec.BaselineDirection != alerts.NrqlBaselineDirections.LowerOnly && *condition.Spec.BaselineDirection != alerts.NrqlBaselineDirections.UpperAndLower {
					condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("baselineDirection"), "baselineDirection must be one of: UPPER_ONLY | LOWER_ONLY | UPPER_AND_LOWER"))
				}
			} else {
				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("baselineDirection"), "baselineDirection must be set when type is baseline"))
			}
		}

		if condition.Spec.Type == "STATIC" {
			if condition.Spec.BaselineDirection != nil {
				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("baselineDirection"), "baselineDirection does not apply to static conditions. Remove this field from config"))
			}
		}

		if condition.Spec.Terms != nil {
			for t, term := range condition.Spec.Terms {
				if term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.ABOVE &&
					term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.ABOVE_OR_EQUALS &&
					term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.BELOW &&
					term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.BELOW_OR_EQUALS &&
					term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.EQUALS &&
					term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.NOT_EQUALS {

					condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("operator"), "operator must be one of: ABOVE | ABOVE_OR_EQAULS | BELOW | BELOW_OR_EQAULS | EQUALS | NOT_EQUALS"))
				}

				if term.Threshold == "" {
					condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("threshold"), "threshold must be set"))
				}

				if term.Priority != "CRITICAL" && term.Priority != "WARNING" {
					condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("priority"), "priority must be one of: CRITICAL | WARNING"))
				}

				if term.ThresholdOccurrences != "ALL" && term.ThresholdOccurrences != "AT_LEAST_ONCE" {
					condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdOccurrences"), "thresholdOccurrences must be one of: ALL | AT_LEAST_ONCE"))
				}

				if !signalSet {
					//baseline conditions must have a minimum of 120 seconds thresholdDuration for some reason
					if condition.Spec.Type == "BASELINE" {
						if term.ThresholdDuration < 120 || term.ThresholdDuration > 86400 {
							condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "baseline thresholdDuration must be within 120-86400 seconds when baseline type is selected, and must be a multiple of the aggregation window [default: 60]"))
						}
						if !r.IsMultiple(term.ThresholdDuration, 120) {
							condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "baseline thresholdDuration must be a multiple of the aggregation window [default: 60]"))
						}
					} else {
						if !r.IsMultiple(term.ThresholdDuration, 60) {
							condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "static thresholdDuration must be a multiple of the aggregation window [default: 60]"))
						}
					}
				} else {
					if condition.Spec.Type == "BASELINE" {
						if term.ThresholdDuration < 120 || term.ThresholdDuration > 86400 {
							condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "baseline thresholdDuration must be within 120-86400 seconds when baseline type is selected"))
						}
					}

					if !r.IsMultiple(term.ThresholdDuration, *condition.Spec.Signal.AggregationWindow) {
						condErrs = append(condErrs, field.TypeInvalid(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "thresholdDuration must be a multiple of the aggregation window ("+strconv.Itoa(*condition.Spec.Signal.AggregationWindow)+")"))
					}
				}

				// if term.ThresholdDuration < 60 || term.ThresholdDuration > 86400 {
				// 	condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("conditions").Index(i).Child("spec").Child("terms").Index(t).Child("thresholdDuration"), "thresholdDuration must be within 60-86400 seconds and must be a multiple of the aggregation window [default: 60]"))
				// }
			}
		}

		if signalSet {
			if *condition.Spec.Signal.AggregationMethod != alerts.NrqlConditionAggregationMethodTypes.EventFlow && *condition.Spec.Signal.AggregationMethod != alerts.NrqlConditionAggregationMethodTypes.EventTimer && *condition.Spec.Signal.AggregationMethod != alerts.NrqlConditionAggregationMethodTypes.Cadence {
				condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationMethod"), condition.Spec.Signal.AggregationMethod, "aggregationMethod must be one of: EVENT_FLOW | EVENT_TIMER | CADENCE"))
			}

			if condition.Spec.Signal.EvaluationOffset != nil {
				condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("evaluationOffset"), condition.Spec.Signal.EvaluationOffset, "evaluationOffset is deprecated - aggregationDelay or aggregationTimer should be used instead"))
			}

			//event flow/cadence specific validation
			if *condition.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.EventFlow || *condition.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.Cadence {
				if condition.Spec.Signal.AggregationTimer != nil {
					condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationTimer"), condition.Spec.Signal.AggregationTimer, "aggregationTimer is only applicable when aggregationMethod is EVENT_TIMER"))
				}

				if condition.Spec.Signal.AggregationDelay == nil {
					condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationDelay"), condition.Spec.Signal.AggregationDelay, "aggregationDelay must be supplied when aggregationMethod is EVENT_FLOW or CADENCE"))
				} else {
					if *condition.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.EventFlow {
						if *condition.Spec.Signal.AggregationDelay > 1200 {
							condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationDelay"), condition.Spec.Signal.AggregationDelay, "aggregationDelay cannot be more than 1200 seconds"))
						}
					}

					if *condition.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.Cadence {
						if *condition.Spec.Signal.AggregationDelay > 3600 {
							condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationDelay"), condition.Spec.Signal.AggregationDelay, "aggregationDelay cannot be more than 3600 seconds"))
						}
					}
				}
			}

			if *condition.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.EventTimer {
				if condition.Spec.Signal.AggregationDelay != nil {
					condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationDelay"), condition.Spec.Signal.AggregationDelay, "aggregationDelay is only applicable when aggregationMethod is EVENT_FLOW OR CADENCE"))
				}

				if condition.Spec.Signal.AggregationTimer == nil {
					condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationTimer"), condition.Spec.Signal.AggregationTimer, "aggregationTimer must be supplied when aggregationMethod is EVENT_TIMER"))
				} else {
					if *condition.Spec.Signal.AggregationTimer < 5 || *condition.Spec.Signal.AggregationTimer > 1200 {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("conditions").Index(i).Child("signal").Child("aggregationTimer"), condition.Spec.Signal.AggregationTimer, "aggregationTimer must be between 5-1200 seconds"))
					}
				}
			}
		}
	}

	return condErrs
}

// ValidateTypeSwitch validates if type is attempted to be switched from static -> baseline or vice versa
func (r *AlertPolicy) ValidateTypeSwitch(oldPolicy *AlertPolicy) field.ErrorList {
	var typeErrs field.ErrorList
	currentConditions := r.Spec.Conditions
	oldConditions := oldPolicy.Spec.Conditions

	for c, currentCond := range currentConditions {
		for _, oldCond := range oldConditions {
			if currentCond.Spec.Name == oldCond.Spec.Name {
				if (currentCond.Spec.Type == "BASELINE" && oldCond.Spec.Type == "STATIC") || (currentCond.Spec.Type == "STATIC" && oldCond.Spec.Type == "BASELINE") {
					typeErrs = append(typeErrs, field.TypeInvalid(field.NewPath("spec").Child("conditions").Index(c).Child("spec").Child("type"), currentCond.Spec.Type, "Cannot switch condition types after condition created. Delete the current condition and recreate a new one with the desired type."))
				}
				break
			}
		}
	}

	return typeErrs
}

// IsMultiple validates that a given thresholdDuration is a multiple of the signal.aggregationWindow configured
func (r *AlertPolicy) IsMultiple(input int, divisor int) bool {
	if divisor == 0 {
		return false
	}

	if input%divisor == 0 {
		return true
	} else {
		return false
	}
}
