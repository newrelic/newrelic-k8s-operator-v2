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
var alertchannellog = logf.Log.WithName("alertchannel-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *AlertChannel) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-alertchannel,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertchannels,verbs=create;update,versions=v1,name=malertchannel.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &AlertChannel{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *AlertChannel) Default() {
	alertchannellog.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-alertchannel,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertchannels,verbs=create;update,versions=v1,name=valertchannel.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &AlertChannel{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertChannel) ValidateCreate() (admission.Warnings, error) {
	alertchannellog.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateChannelSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertChannel"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertChannel) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	alertchannellog.Info("validate update", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateChannelSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertChannel"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *AlertChannel) ValidateDelete() (admission.Warnings, error) {
	alertchannellog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *AlertChannel) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *AlertChannel) ValidateAPIKeyOrSecret() field.ErrorList {
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

func (r *AlertChannel) ValidateChannelSchema() field.ErrorList {
	var channelErrs field.ErrorList

	if r.Spec.Name == "" {
		channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("name"), "channel name must be set"))
	}

	if r.Spec.DestinationName == "" {
		channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("destinationName"), "a destination name must be set to associate channel with"))
	}

	if r.Spec.Product != "IINT" {
		channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("product"), "product must be set to `IINT`"))
	}

	if r.Spec.Type == "" {
		channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("type"), "channel type must be set"))
	} else {
		if r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.EMAIL &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.EVENT_BRIDGE &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.JIRA_CLASSIC &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.JIRA_NEXTGEN &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.MOBILE_PUSH &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_ACCOUNT_INTEGRATION &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_SERVICE_INTEGRATION &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SERVICE_NOW_APP &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SERVICENOW_INCIDENTS &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SLACK &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SLACK_COLLABORATION &&
			r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.WEBHOOK {
			channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("type"), r.Spec.Type, "invalid channel type - must be one of: EMAIL | EVENT_BRIDGE | JIRA_CLASSIC | JIRA_NEXTGEN | MOBILE_PUSH | PAGERDUTY_SERVICE_INTEGRATION | PAGERDUTY_ACCOUNT_INTEGRATION | SERVICE_NOW_APP | SERVICENOW_INCIDENTS | SLACK | SLACK_COLLABORATION | WEBHOOK"))
		}
	}

	//properties validation
	if r.Spec.Properties != nil {
		if len(r.Spec.Properties) > 0 {
			for i, prop := range r.Spec.Properties {
				if prop.Key == "" && r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.MOBILE_PUSH {
					channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("properties").Index(i).Child("key"), "at least 1 property key must be set"))
				}

				if prop.Value == "" && r.Spec.Type != notifications.AiNotificationsChannelTypeTypes.MOBILE_PUSH {
					channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("properties").Index(i).Child("value"), "at least 1 property value must be set"))
				}

				switch r.Spec.Type {
				case notifications.AiNotificationsChannelTypeTypes.EMAIL:
					if prop.Key != "subject" && prop.Key != "customDetailsEmail" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be one of `subject` or `customDetailsEmail` for email type channels"))
					}
				case notifications.AiNotificationsChannelTypeTypes.EVENT_BRIDGE:
					if prop.Key != "eventSource" && prop.Key != "eventContent" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be one of `eventSource` or `eventContent` for event bridge channels"))
					}
				case notifications.AiNotificationsChannelTypeTypes.JIRA_CLASSIC:
					if prop.Key != "project" && prop.Key != "issueType" && prop.Key != "description" && prop.Key != "summary" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: project | issueType | description | summary"))
					}
				case notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_SERVICE_INTEGRATION:
					if prop.Key != "summary" && prop.Key != "customDetails" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: summary | customDetails"))
					}
				case notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_ACCOUNT_INTEGRATION:
					if prop.Key != "summary" && prop.Key != "service" && prop.Key != "email" && prop.Key != "customDetails" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: summary | service | email | customDetails"))
					}
				case notifications.AiNotificationsChannelTypeTypes.SLACK:
					if prop.Key != "channelId" && prop.Key != "customDetailsSlack" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: channelId | customDetailsSlack"))
					}
				case notifications.AiNotificationsChannelTypeTypes.SLACK_COLLABORATION:
					if prop.Key != "channelId" && prop.Key != "customDetailsSlack" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: channelId | customDetailsSlack"))
					}
				case notifications.AiNotificationsChannelTypeTypes.WEBHOOK:
					if prop.Key != "payload" {
						channelErrs = append(channelErrs, field.TypeInvalid(field.NewPath("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be `payload` for webhook type destinations"))
					}
				}
			}
		} else {
			channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("properties"), "properties need at least 1 key-value pair defined"))
		}
	} else {
		channelErrs = append(channelErrs, field.Required(field.NewPath("spec").Child("properties"), "properties must be set"))
	}

	return channelErrs
}
