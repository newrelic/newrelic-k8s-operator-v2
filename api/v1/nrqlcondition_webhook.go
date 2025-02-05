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
var nrqlconditionlog = logf.Log.WithName("nrqlcondition-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *NrqlCondition) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-nrqlcondition,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=nrqlconditions,verbs=create;update,versions=v1,name=mnrqlcondition.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &NrqlCondition{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *NrqlCondition) Default() {
	nrqlconditionlog.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-nrqlcondition,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=nrqlconditions,verbs=create;update,versions=v1,name=vnrqlcondition.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &NrqlCondition{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *NrqlCondition) ValidateCreate() (admission.Warnings, error) {
	nrqlconditionlog.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateConditionSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "NrqlCondition"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *NrqlCondition) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	nrqlconditionlog.Info("validate update", "name", r.Name)
	var allErrs field.ErrorList
	var prevCondition = old.(*NrqlCondition)

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateConditionSchema()...)
	allErrs = append(allErrs, r.ValidateTypeSwitch(prevCondition)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "NrqlCondition"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *NrqlCondition) ValidateDelete() (admission.Warnings, error) {
	nrqlconditionlog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *NrqlCondition) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *NrqlCondition) ValidateAPIKeyOrSecret() field.ErrorList {
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

// ValidateConditionSchema validates fields for a nrql condition
func (r *NrqlCondition) ValidateConditionSchema() field.ErrorList {
	var condErrs field.ErrorList

	var signalSet = r.Spec.Signal != nil

	if r.Spec.ExistingPolicyID == "" {
		condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("existingPolicyId"), "existingPolicyId must be set"))
	}

	if r.Spec.Name == "" {
		condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("name"), "condition name must be set"))
	}

	if r.Spec.Type != "BASELINE" && r.Spec.Type != "STATIC" {
		condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("type"), "condition type must be one of: STATIC | BASELINE"))
	}

	if r.Spec.Type == "BASELINE" {
		if r.Spec.BaselineDirection != nil {
			if *r.Spec.BaselineDirection != alerts.NrqlBaselineDirections.UpperOnly && *r.Spec.BaselineDirection != alerts.NrqlBaselineDirections.LowerOnly && *r.Spec.BaselineDirection != alerts.NrqlBaselineDirections.UpperAndLower {
				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("baselineDirection"), "baselineDirection must be one of: UPPER_ONLY | LOWER_ONLY | UPPER_AND_LOWER"))
			}
		} else {
			condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("baselineDirection"), "baselineDirection must be set when type is baseline"))
		}
	}

	if r.Spec.Type == "STATIC" {
		if r.Spec.BaselineDirection != nil {
			condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("baselineDirection"), "baselineDirection does not apply to static conditions. Remove this field from config"))
		}
	}

	if r.Spec.Terms != nil {
		for t, term := range r.Spec.Terms {
			if term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.ABOVE &&
				term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.ABOVE_OR_EQUALS &&
				term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.BELOW &&
				term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.BELOW_OR_EQUALS &&
				term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.EQUALS &&
				term.Operator != alerts.AlertsNRQLConditionTermsOperatorTypes.NOT_EQUALS {

				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("terms").Index(t).Child("operator"), "operator must be one of: ABOVE | ABOVE_OR_EQAULS | BELOW | BELOW_OR_EQAULS | EQUALS | NOT_EQUALS"))
			}

			if term.Threshold == "" {
				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("terms").Index(t).Child("threshold"), "threshold must be set"))
			}

			if term.Priority != "CRITICAL" && term.Priority != "WARNING" {
				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("terms").Index(t).Child("priority"), "priority must be one of: CRITICAL | WARNING"))
			}

			if term.ThresholdOccurrences != "ALL" && term.ThresholdOccurrences != "AT_LEAST_ONCE" {
				condErrs = append(condErrs, field.Required(field.NewPath("spec").Child("terms").Index(t).Child("thresholdOccurrences"), "thresholdOccurrences must be one of: ALL | AT_LEAST_ONCE"))
			}

			if !signalSet {
				//baseline conditions must have a minimum of 120 seconds thresholdDuration for some reason
				if r.Spec.Type == "BASELINE" {
					if term.ThresholdDuration < 120 || term.ThresholdDuration > 86400 {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "baseline thresholdDuration must be within 120-86400 seconds when baseline type is selected, and must be a multiple of the aggregation window [default: 60]"))
					}
					if !r.IsMultiple(term.ThresholdDuration, 120) {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "baseline thresholdDuration must be a multiple of the aggregation window [default: 60]"))
					}
				} else {
					if !r.IsMultiple(term.ThresholdDuration, 60) {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "static thresholdDuration must be a multiple of the aggregation window [default: 60]"))
					}
				}
			} else {
				if r.Spec.Type == "BASELINE" {
					if term.ThresholdDuration < 120 || term.ThresholdDuration > 86400 {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "baseline thresholdDuration must be within 120-86400 seconds when baseline type is selected"))
					}
				}

				if !r.IsMultiple(term.ThresholdDuration, *r.Spec.Signal.AggregationWindow) {
					condErrs = append(condErrs, field.TypeInvalid(field.NewPath("spec").Child("terms").Index(t).Child("thresholdDuration"), term.ThresholdDuration, "thresholdDuration must be a multiple of the aggregation window ("+strconv.Itoa(*r.Spec.Signal.AggregationWindow)+")"))
				}
			}
		}
	}

	if signalSet {
		if *r.Spec.Signal.AggregationMethod != alerts.NrqlConditionAggregationMethodTypes.EventFlow && *r.Spec.Signal.AggregationMethod != alerts.NrqlConditionAggregationMethodTypes.EventTimer && *r.Spec.Signal.AggregationMethod != alerts.NrqlConditionAggregationMethodTypes.Cadence {
			condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationMethod"), r.Spec.Signal.AggregationMethod, "aggregationMethod must be one of: EVENT_FLOW | EVENT_TIMER | CADENCE"))
		}

		if r.Spec.Signal.EvaluationOffset != nil {
			condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("evaluationOffset"), r.Spec.Signal.EvaluationOffset, "evaluationOffset is deprecated - aggregationDelay or aggregationTimer should be used instead"))
		}

		//event flow/cadence specific validation
		if *r.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.EventFlow || *r.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.Cadence {
			if r.Spec.Signal.AggregationTimer != nil {
				condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationTimer"), r.Spec.Signal.AggregationTimer, "aggregationTimer is only applicable when aggregationMethod is EVENT_TIMER"))
			}

			if r.Spec.Signal.AggregationDelay == nil {
				condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationDelay"), r.Spec.Signal.AggregationDelay, "aggregationDelay must be supplied when aggregationMethod is EVENT_FLOW or CADENCE"))
			} else {
				if *r.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.EventFlow {
					if *r.Spec.Signal.AggregationDelay > 1200 {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationDelay"), r.Spec.Signal.AggregationDelay, "aggregationDelay cannot be more than 1200 seconds"))
					}
				}

				if *r.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.Cadence {
					if *r.Spec.Signal.AggregationDelay > 3600 {
						condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationDelay"), r.Spec.Signal.AggregationDelay, "aggregationDelay cannot be more than 3600 seconds"))
					}
				}
			}
		}

		//event timer specific validation
		if *r.Spec.Signal.AggregationMethod == alerts.NrqlConditionAggregationMethodTypes.EventTimer {
			if r.Spec.Signal.AggregationDelay != nil {
				condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationDelay"), r.Spec.Signal.AggregationDelay, "aggregationDelay is only applicable when aggregationMethod is EVENT_FLOW OR CADENCE"))
			}

			if r.Spec.Signal.AggregationTimer == nil {
				condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationTimer"), r.Spec.Signal.AggregationTimer, "aggregationTimer must be supplied when aggregationMethod is EVENT_TIMER"))
			} else {
				if *r.Spec.Signal.AggregationTimer < 5 || *r.Spec.Signal.AggregationTimer > 1200 {
					condErrs = append(condErrs, field.Invalid(field.NewPath("spec").Child("signal").Child("aggregationTimer"), r.Spec.Signal.AggregationTimer, "aggregationTimer must be between 5-1200 seconds"))
				}
			}
		}
	}

	return condErrs
}

// ValidateTypeSwitch validates if type is attempted to be switched from static -> baseline or vice versa
func (r *NrqlCondition) ValidateTypeSwitch(oldCondition *NrqlCondition) field.ErrorList {
	var typeErrs field.ErrorList
	currentCondition := r.Spec

	if (currentCondition.Type == "BASELINE" && oldCondition.Spec.Type == "STATIC") || (currentCondition.Type == "STATIC" && oldCondition.Spec.Type == "BASELINE") {
		typeErrs = append(typeErrs, field.TypeInvalid(field.NewPath("spec").Child("type"), currentCondition.Type, "Cannot switch condition types after condition created. Delete the current condition and recreate a new one with the desired type."))
	}

	return typeErrs
}

// IsMultiple validates that a given thresholdDuration is a multiple of the signal.aggregationWindow configured
func (r *NrqlCondition) IsMultiple(input int, divisor int) bool {
	if divisor == 0 {
		return false
	}

	if input%divisor == 0 {
		return true
	} else {
		return false
	}
}
