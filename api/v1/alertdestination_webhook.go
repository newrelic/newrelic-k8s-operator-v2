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
	"github.com/newrelic/newrelic-client-go/v2/pkg/notifications"
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
var alertdestinationlog = logf.Log.WithName("alertdestination-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *AlertDestination) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-alertdestination,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertdestinations,verbs=create;update,versions=v1,name=malertdestination.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &AlertDestination{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *AlertDestination) Default() {
	alertdestinationlog.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-alertdestination,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertdestinations,verbs=create;update,versions=v1,name=valertdestination.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &AlertDestination{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertDestination) ValidateCreate() (admission.Warnings, error) {
	alertdestinationlog.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateDestinationSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertDestination"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertDestination) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	alertdestinationlog.Info("validate update", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateDestinationSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertDestination"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *AlertDestination) ValidateDelete() (admission.Warnings, error) {
	alertdestinationlog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *AlertDestination) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *AlertDestination) ValidateAPIKeyOrSecret() field.ErrorList {
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

func (r *AlertDestination) ValidateDestinationSchema() field.ErrorList {
	var destErrors field.ErrorList

	if r.Spec.Name == "" {
		destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("name"), "destination name must be set"))
	}

	if r.Spec.Type == "" {
		destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("type"), "destination type must be set"))
	} else {
		if r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.EMAIL &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.EVENT_BRIDGE &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.JIRA &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.MOBILE_PUSH &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.PAGERDUTY_ACCOUNT_INTEGRATION &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.PAGERDUTY_SERVICE_INTEGRATION &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.SERVICE_NOW_APP &&
			r.Spec.Type != notifications.AiNotificationsDestinationTypeTypes.WEBHOOK {
			destErrors = append(destErrors, field.TypeInvalid(field.NewPath("spec").Child("type"), r.Spec.Type, "invalid destination type - must be one of: EMAIL | EVENT_BRIDGE | JIRA | MOBILE_PUSH | PAGERDUTY_SERVICE_INTEGRATION | PAGERDUTY_ACCOUNT_INTEGRATION | SERVICE_NOW_APP | WEBHOOK"))
		}
	}

	//properties validation
	if r.Spec.Properties == nil {
		destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("properties"), "properties must be set"))
	} else {
		if len(r.Spec.Properties) > 0 {
			for i, prop := range r.Spec.Properties {
				if prop.Key == "" {
					destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("properties").Index(i).Child("key"), "at least 1 property key must be set"))
				}

				if prop.Value == "" {
					destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("properties").Index(i).Child("key"), "at least 1 property value must be set"))
				}

				switch r.Spec.Type {
				case notifications.AiNotificationsDestinationTypeTypes.EMAIL:
					if prop.Key != "email" {
						destErrors = append(destErrors, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be `email` for email type destinations"))
					}
				case notifications.AiNotificationsDestinationTypeTypes.EVENT_BRIDGE:
					if prop.Key != "AWSAccountId" && prop.Key != "AWSRegion" {
						destErrors = append(destErrors, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "required keys `AWSAccountId` and `AWSRegion` must be specified for event bridge destinations"))
					}
				case notifications.AiNotificationsDestinationTypeTypes.JIRA, notifications.AiNotificationsDestinationTypeTypes.SERVICE_NOW_APP:
					if prop.Key != "url" && prop.Key != "two_way_integration" {
						destErrors = append(destErrors, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "a url property must be defined at minimum. the only keys that can be defined for Jira or ServiceNow props are `url` and `two_way_integration`"))
					}
				case notifications.AiNotificationsDestinationTypeTypes.MOBILE_PUSH:
					if prop.Key != "userId" {
						destErrors = append(destErrors, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be `userId` for mobile push type destinations"))
					}
				case notifications.AiNotificationsDestinationTypeTypes.WEBHOOK:
					if prop.Key != "url" {
						destErrors = append(destErrors, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be `url` for webhook type destinations"))
					}
				}
			}
		} else {
			destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("properties"), "properties need at least 1 key-value pair defined"))
		}
	}

	//auth validation
	if r.Spec.Type == notifications.AiNotificationsDestinationTypeTypes.JIRA || r.Spec.Type == notifications.AiNotificationsDestinationTypeTypes.SERVICE_NOW_APP || r.Spec.Type == notifications.AiNotificationsDestinationTypeTypes.PAGERDUTY_ACCOUNT_INTEGRATION || r.Spec.Type == notifications.AiNotificationsDestinationTypeTypes.PAGERDUTY_SERVICE_INTEGRATION {
		if r.Spec.Auth == nil {
			destErrors = append(destErrors, field.Required(field.NewPath("spec").Child("auth"), "auth block must be set"))
		}
	}

	return destErrors
}
