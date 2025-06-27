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
var entitytagginglog = logf.Log.WithName("entitytagging-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *EntityTagging) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-entitytagging,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=entitytaggings,verbs=create;update,versions=v1,name=mentitytagging.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &EntityTagging{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *EntityTagging) Default() {
	entitytagginglog.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-entitytagging,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=entitytaggings,verbs=create;update,versions=v1,name=ventitytagging.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &EntityTagging{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *EntityTagging) ValidateCreate() (admission.Warnings, error) {
	entitytagginglog.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateTaggingSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "EntityTagging"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *EntityTagging) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	entitytagginglog.Info("validate update", "name", r.Name)
	var allErrs field.ErrorList
	oldEntity := old.(*EntityTagging)

	if r.Spec.Region != oldEntity.Spec.Region {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("region"), r.Spec.Region, "region is immutable and cannot be changed after creation"))
	}

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateTaggingSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "EntityTagging"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *EntityTagging) ValidateDelete() (admission.Warnings, error) {
	entitytagginglog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *EntityTagging) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *EntityTagging) ValidateAPIKeyOrSecret() field.ErrorList {
	var keyErrs field.ErrorList

	apiKeySet := r.Spec.APIKey != ""
	apiKeySecretSet := r.Spec.APIKeySecret != (NewRelicSecret{})

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

func (r *EntityTagging) ValidateTaggingSchema() field.ErrorList {
	var taggingErrs field.ErrorList

	validRegions := map[string]bool{"US": true, "EU": true}
	if !validRegions[r.Spec.Region] {
		taggingErrs = append(taggingErrs, field.NotSupported(field.NewPath("spec").Child("region"), r.Spec.Region, []string{"US", "EU"}))
	}

	if len(r.Spec.EntityNames) == 0 {
		taggingErrs = append(taggingErrs, field.Required(field.NewPath("spec").Child("entityNames"), "at least one entityName must be defined"))
	} else {
		for e, name := range r.Spec.EntityNames {
			if name == "" {
				taggingErrs = append(taggingErrs, field.Invalid(field.NewPath("spec").Child("entityNames").Index(e), name, "entity names cannot be empty strings"))
			}
		}
	}

	if len(r.Spec.Tags) == 0 {
		taggingErrs = append(taggingErrs, field.Required(field.NewPath("spec").Child("tags"), "at least one tag must be defined"))
	}

	if r.Spec.Tags != nil {
		for i, tag := range r.Spec.Tags {
			if tag.Key == "" {
				taggingErrs = append(taggingErrs, field.Required(field.NewPath("spec").Child("tags").Index(i).Child("key"), "at least one tag key must be defined"))
			}

			if len(tag.Key) > 128 {
				taggingErrs = append(taggingErrs, field.Invalid(field.NewPath("spec").Child("tags").Index(i).Child("key"), tag.Key, "key length cannot exceed 128 characters"))
			}

			if len(tag.Values) == 0 {
				taggingErrs = append(taggingErrs, field.Required(field.NewPath("spec").Child("tags").Index(i).Child("values"), "at least one tag value must be defined"))
			}

			for v, value := range tag.Values {
				if len(value) > 256 {
					taggingErrs = append(taggingErrs, field.Invalid(field.NewPath("spec").Child("tags").Index(i).Child("values").Index(v), value, "value length cannot exceed 256 characters"))
				}
			}
		}
	}

	return taggingErrs

}
