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

	"github.com/newrelic/newrelic-client-go/v2/pkg/servicelevel"
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
var servicelevellog = logf.Log.WithName("servicelevel-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *ServiceLevel) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-servicelevel,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=servicelevels,verbs=create;update,versions=v1,name=mservicelevel.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &ServiceLevel{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *ServiceLevel) Default() {
	servicelevellog.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-servicelevel,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=servicelevels,verbs=create;update,versions=v1,name=vservicelevel.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &ServiceLevel{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *ServiceLevel) ValidateCreate() (admission.Warnings, error) {
	servicelevellog.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateServiceLevelSchema()...)
	allErrs = append(allErrs, r.ValidateEventsSchema()...)
	allErrs = append(allErrs, r.ValidateObjectivesSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "ServiceLevel"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *ServiceLevel) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	servicelevellog.Info("validate update", "name", r.Name)
	var allErrs field.ErrorList
	oldServiceLevel := old.(*ServiceLevel)

	allErrs = append(allErrs, r.ValidateImmutableFields(oldServiceLevel)...)
	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateServiceLevelSchema()...)
	allErrs = append(allErrs, r.ValidateEventsSchema()...)
	allErrs = append(allErrs, r.ValidateObjectivesSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "ServiceLevel"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *ServiceLevel) ValidateDelete() (admission.Warnings, error) {
	servicelevellog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *ServiceLevel) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}

	r.Spec.Name = strings.TrimSpace(r.Spec.Name)
	r.Spec.Description = strings.TrimSpace(r.Spec.Description)

	applyEventsQueryDefaults(r.Spec.Events.ValidEvents)
	applyEventsQueryDefaults(r.Spec.Events.GoodEvents)
	applyEventsQueryDefaults(r.Spec.Events.BadEvents)

	for i := range r.Spec.Objectives {
		obj := &r.Spec.Objectives[i]
		obj.Name = strings.TrimSpace(obj.Name)
		obj.Description = strings.TrimSpace(obj.Description)
		obj.Target = NormalizeDecimal(obj.Target)

		// No default for rolling.count: 1/7/28 are materially different SLOs -
		// guessing gives someone a window they never asked for.
		if obj.TimeWindow.Rolling.Unit == "" {
			obj.TimeWindow.Rolling.Unit = servicelevel.ServiceLevelObjectiveRollingTimeWindowUnitTypes.DAY
		}
	}
}

// applyEventsQueryDefaults defaults a single events query in place. Deliberately
// does not strip an illegal attribute/threshold combination - the validator
// rejects those instead. Silently deleting a field the user wrote is worse than
// refusing it.
func applyEventsQueryDefaults(query *ServiceLevelEventsQuery) {
	if query == nil {
		return
	}

	query.From = servicelevel.NRQL(NormalizeNRQL(string(query.From)))
	query.Where = servicelevel.NRQL(NormalizeNRQL(string(query.Where)))

	if query.Select == nil {
		query.Select = &ServiceLevelEventsQuerySelect{
			Function: servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.COUNT,
		}
		return
	}

	if query.Select.Function == "" {
		query.Select.Function = servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.COUNT
	}

	if query.Select.Threshold != "" {
		query.Select.Threshold = NormalizeDecimal(query.Select.Threshold)
	}
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *ServiceLevel) ValidateAPIKeyOrSecret() field.ErrorList {
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

// ValidateServiceLevelSchema validates the top-level ServiceLevel fields
func (r *ServiceLevel) ValidateServiceLevelSchema() field.ErrorList {
	var errs field.ErrorList

	validRegions := map[string]bool{"US": true, "EU": true}
	if !validRegions[r.Spec.Region] {
		errs = append(errs, field.NotSupported(field.NewPath("spec").Child("region"), r.Spec.Region, []string{"US", "EU"}))
	}

	if r.Spec.Name == "" {
		errs = append(errs, field.Required(field.NewPath("spec").Child("name"), "name must be set"))
	}

	if r.Spec.EntityGUID == "" {
		errs = append(errs, field.Required(field.NewPath("spec").Child("entityGuid"), "entityGuid must be set"))
	}

	return errs
}

// ValidateEventsSchema validates the events block (validEvents/goodEvents/badEvents)
func (r *ServiceLevel) ValidateEventsSchema() field.ErrorList {
	var errs field.ErrorList

	events := r.Spec.Events
	path := field.NewPath("spec").Child("events")

	if events.ValidEvents == nil {
		errs = append(errs, field.Required(path.Child("validEvents"), "validEvents must be set"))
	}

	switch {
	case events.GoodEvents != nil && events.BadEvents != nil:
		errs = append(errs, field.Invalid(path, nil, "only one of goodEvents or badEvents can be set"))
	case events.GoodEvents == nil && events.BadEvents == nil:
		errs = append(errs, field.Required(path, "one of goodEvents or badEvents must be set"))
	}

	errs = append(errs, validateEventsQuery(path.Child("validEvents"), events.ValidEvents)...)
	errs = append(errs, validateEventsQuery(path.Child("goodEvents"), events.GoodEvents)...)
	errs = append(errs, validateEventsQuery(path.Child("badEvents"), events.BadEvents)...)

	return errs
}

// validateEventsQuery validates a single events query. Nil-safe so all three
// callers in ValidateEventsSchema pass unconditionally when unset.
func validateEventsQuery(path *field.Path, query *ServiceLevelEventsQuery) field.ErrorList {
	var errs field.ErrorList

	if query == nil {
		return errs
	}

	if query.From == "" {
		errs = append(errs, field.Required(path.Child("from"), "from must be set"))
	}

	if query.Select == nil {
		return errs
	}

	function := query.Select.Function
	switch function {
	case servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.COUNT:
		if query.Select.Attribute != "" {
			errs = append(errs, field.Invalid(path.Child("select").Child("attribute"), query.Select.Attribute,
				"attribute must not be set when function is COUNT - New Relic silently drops it, causing a permanent update loop"))
		}
	case servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.SUM,
		servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.GET_FIELD:
		if query.Select.Attribute == "" {
			errs = append(errs, field.Required(path.Child("select").Child("attribute"), "attribute must be set for SUM/GET_FIELD"))
		}
	case servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.GET_CDF_COUNT:
		if query.Select.Attribute == "" {
			errs = append(errs, field.Required(path.Child("select").Child("attribute"), "attribute must be set for GET_CDF_COUNT"))
		}
		if query.Select.Threshold == "" {
			errs = append(errs, field.Required(path.Child("select").Child("threshold"), "threshold must be set for GET_CDF_COUNT"))
		}
	default:
		errs = append(errs, field.NotSupported(path.Child("select").Child("function"), function,
			[]string{"COUNT", "SUM", "GET_FIELD", "GET_CDF_COUNT"}))
	}

	if function != servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.GET_CDF_COUNT && query.Select.Threshold != "" {
		errs = append(errs, field.Invalid(path.Child("select").Child("threshold"), query.Select.Threshold,
			"threshold can only be set when function is GET_CDF_COUNT"))
	}

	if query.Select.Threshold != "" {
		if _, err := strconv.ParseFloat(query.Select.Threshold, 64); err != nil {
			errs = append(errs, field.Invalid(path.Child("select").Child("threshold"), query.Select.Threshold,
				"threshold must be a valid decimal number"))
		}
	}

	return errs
}

// ValidateObjectivesSchema validates the objectives slice. Forward-compatible
// with a future NerdGraph relaxation: enforces len == 1 here rather than in the
// CRD schema, so no CRD break is needed if NerdGraph ever allows multiple.
func (r *ServiceLevel) ValidateObjectivesSchema() field.ErrorList {
	var errs field.ErrorList
	path := field.NewPath("spec").Child("objectives")

	switch {
	case len(r.Spec.Objectives) == 0:
		errs = append(errs, field.Required(path, "exactly one objective must be defined"))
		return errs
	case len(r.Spec.Objectives) > 1:
		errs = append(errs, field.TooMany(path, len(r.Spec.Objectives), 1))
	}

	validCounts := map[int]bool{1: true, 7: true, 28: true}

	for i, obj := range r.Spec.Objectives {
		objPath := path.Index(i)

		target, err := strconv.ParseFloat(obj.Target, 64)
		if err != nil {
			errs = append(errs, field.Invalid(objPath.Child("target"), obj.Target, "target must be a valid decimal number"))
		} else {
			if target < 0 || target > 100 {
				errs = append(errs, field.Invalid(objPath.Child("target"), obj.Target, "target must be between 0 and 100"))
			}

			// NR rounds targets past 5 decimals. Anything more precise creates a
			// permanent drift loop: create sends the precise value, NR stores the
			// rounded one, the next reconcile sees a mismatch and updates, forever.
			if decimalPlaces(NormalizeDecimal(obj.Target)) > 5 {
				errs = append(errs, field.Invalid(objPath.Child("target"), obj.Target,
					"target must not have more than 5 decimal places - New Relic rounds beyond that, causing a permanent update loop"))
			}
		}

		rolling := obj.TimeWindow.Rolling
		if !validCounts[rolling.Count] {
			errs = append(errs, field.Invalid(objPath.Child("timeWindow").Child("rolling").Child("count"), rolling.Count,
				"count must be one of 1, 7, 28"))
		}

		if rolling.Unit != servicelevel.ServiceLevelObjectiveRollingTimeWindowUnitTypes.DAY {
			errs = append(errs, field.NotSupported(objPath.Child("timeWindow").Child("rolling").Child("unit"), rolling.Unit,
				[]string{"DAY"}))
		}
	}

	return errs
}

// decimalPlaces returns the number of digits after the decimal point in a
// canonical (non-scientific) decimal string.
func decimalPlaces(value string) int {
	idx := strings.IndexByte(value, '.')
	if idx == -1 {
		return 0
	}

	return len(value) - idx - 1
}

// ValidateImmutableFields rejects changes to entityGuid, accountId and region.
// name and the event query bodies stay mutable - ServiceLevelIndicatorUpdateInput
// can change all of those.
func (r *ServiceLevel) ValidateImmutableFields(old *ServiceLevel) field.ErrorList {
	var errs field.ErrorList

	if r.Spec.EntityGUID != old.Spec.EntityGUID {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("entityGuid"), r.Spec.EntityGUID,
			"entityGuid is immutable and cannot be changed after creation"))
	}

	if r.Spec.AccountID != old.Spec.AccountID {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("accountId"), r.Spec.AccountID,
			"accountId is immutable and cannot be changed after creation"))
	}

	if r.Spec.Region != old.Spec.Region {
		errs = append(errs, field.Invalid(field.NewPath("spec").Child("region"), r.Spec.Region,
			"region is immutable and cannot be changed after creation"))
	}

	return errs
}
