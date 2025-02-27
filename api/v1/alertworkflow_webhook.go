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
	"github.com/newrelic/newrelic-client-go/v2/pkg/workflows"
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
var alertworkflowlog = logf.Log.WithName("alertworkflow-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *AlertWorkflow) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-alerts-k8s-newrelic-com-v1-alertworkflow,mutating=true,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertworkflows,verbs=create;update,versions=v1,name=malertworkflow.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &AlertWorkflow{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *AlertWorkflow) Default() {
	alertworkflowlog.Info("default", "name", r.Name)

	r.ApplyDefaults()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-alerts-k8s-newrelic-com-v1-alertworkflow,mutating=false,failurePolicy=fail,sideEffects=None,groups=alerts.k8s.newrelic.com,resources=alertworkflows,verbs=create;update,versions=v1,name=valertworkflow.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &AlertWorkflow{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertWorkflow) ValidateCreate() (admission.Warnings, error) {
	alertworkflowlog.Info("validate create", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateWorkflowSchema()...)
	allErrs = append(allErrs, r.ValidateChannelSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertWorkflow"},
			r.Name, allErrs)
	}

	return nil, nil

}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *AlertWorkflow) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	alertworkflowlog.Info("validate update", "name", r.Name)
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.ValidateAPIKeyOrSecret()...)
	allErrs = append(allErrs, r.ValidateWorkflowSchema()...)
	allErrs = append(allErrs, r.ValidateChannelSchema()...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "alerts.k8s.newrelic.com", Kind: "AlertWorkflow"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *AlertWorkflow) ValidateDelete() (admission.Warnings, error) {
	alertworkflowlog.Info("validate delete", "name", r.Name)

	return nil, nil
}

// ApplyDefaults sets default values for required fields if not set
func (r *AlertWorkflow) ApplyDefaults() {
	if r.Spec.Region == "" {
		r.Spec.Region = "US"
	}
}

// ValidateAPIKeyOrSecret validates an api key or secret is configured
func (r *AlertWorkflow) ValidateAPIKeyOrSecret() field.ErrorList {
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

func (r *AlertWorkflow) ValidateWorkflowSchema() field.ErrorList {
	var wkflwErrs field.ErrorList

	if r.Spec.Name == "" {
		wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("name"), "workflow name must be set"))
	}

	if r.Spec.IssuesFilter == nil {
		wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter"), "issuesFilter must be set"))
	} else {
		if r.Spec.IssuesFilter.Name == "" {
			wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("name"), "issuesFilter name must be set"))
		}

		if r.Spec.IssuesFilter.Type != "FILTER" {
			wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("type"), "issuesFilter type must be set to FILTER"))
		}

		if r.Spec.IssuesFilter.Predicates == nil {
			wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("predicates"), "issuesFilter predicates must be set"))
		} else {
			if len(r.Spec.IssuesFilter.Predicates) > 0 {
				for p, pred := range r.Spec.IssuesFilter.Predicates {
					if pred.Attribute == "" {
						wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("predicates").Index(p).Child("attribute"), "predicate attribute must be set"))
					}
					if pred.Operator != workflows.AiWorkflowsOperatorTypes.CONTAINS &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.DOES_NOT_CONTAIN &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.EQUAL &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.DOES_NOT_EQUAL &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.STARTS_WITH &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.ENDS_WITH &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.DOES_NOT_EXACTLY_MATCH &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.EXACTLY_MATCHES &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.IS &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.IS_NOT &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.LESS_OR_EQUAL &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.LESS_THAN &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.GREATER_OR_EQUAL &&
						pred.Operator != workflows.AiWorkflowsOperatorTypes.GREATER_THAN {
						wkflwErrs = append(wkflwErrs, field.TypeInvalid(field.NewPath("spec").Child("issuesFilter").Child("predicates").Index(p).Child("operator"), pred.Operator, "predicate operator must be one of: CONTAINS | DOES_NOT_CONTAIN | EQUAL | DOES_NOT_EQUAL | STARTS_WITH | ENDS_WITH | DOES_NOT_EXACTLY_MATCH | EXACTLY_MATCHES | IS | IS_NOT | LESS_OR_EQUAL | LESS_THAN | GREATER_OR_EQUAL | GREATER_THAN"))
					}

					if pred.Values == nil {
						wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("predicates").Index(p).Child("values"), "predicate values must be set"))
					} else {
						if len(pred.Values) == 0 {
							wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("predicates").Index(p).Child("values"), "at least one predicate value must be set"))
						}
					}
				}
			} else {
				wkflwErrs = append(wkflwErrs, field.Required(field.NewPath("spec").Child("issuesFilter").Child("predicates"), "at lest one predicate must be set"))
			}
		}

		if r.Spec.MutingRulesHandling != workflows.AiWorkflowsMutingRulesHandlingTypes.DONT_NOTIFY_FULLY_MUTED_ISSUES &&
			r.Spec.MutingRulesHandling != workflows.AiWorkflowsMutingRulesHandlingTypes.DONT_NOTIFY_FULLY_OR_PARTIALLY_MUTED_ISSUES &&
			r.Spec.MutingRulesHandling != workflows.AiWorkflowsMutingRulesHandlingTypes.NOTIFY_ALL_ISSUES {
			wkflwErrs = append(wkflwErrs, field.TypeInvalid(field.NewPath("spec").Child("mutingRulesHandling"), r.Spec.MutingRulesHandling, "mutingRulesHandling must be one of: DONT_NOTIFY_FULLY_MUTED_ISSUES | DONT_NOTIFY_FULLY_OR_PARTIALLY_MUTED_ISSUES | NOTIFY_ALL_ISSUES"))
		}
	}

	return wkflwErrs
}

func (r *AlertWorkflow) ValidateChannelSchema() field.ErrorList {
	var chanErrs field.ErrorList

	if r.Spec.Channels == nil {
		chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels"), "channels must be set"))
	} else {
		if len(r.Spec.Channels) > 0 {
			for c, channel := range r.Spec.Channels {
				if channel.NotificationTriggers == nil {
					chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("notificationTriggers"), "notificationTriggers must be set"))
				} else {
					if len(channel.NotificationTriggers) > 0 {
						for n, trigger := range channel.NotificationTriggers {
							if trigger != workflows.AiWorkflowsNotificationTriggerTypes.ACTIVATED &&
								trigger != workflows.AiWorkflowsNotificationTriggerTypes.ACKNOWLEDGED &&
								trigger != workflows.AiWorkflowsNotificationTriggerTypes.CLOSED &&
								trigger != workflows.AiWorkflowsNotificationTriggerTypes.PRIORITY_CHANGED &&
								trigger != workflows.AiWorkflowsNotificationTriggerTypes.OTHER_UPDATES {
								chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("notificationTriggers").Index(n), r.Spec.Channels[c].NotificationTriggers[n], "notificationTrigger must be one of: ACTIVATED | ACKNOWLEDGED | CLOSED | PRIORITY_CHANGED | OTHER_UPDATES"))
							}
						}
					} else {
						chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("notificationTriggers"), "at least one notificationTrigger must be set"))
					}

					if channel.Spec.Name == "" {
						chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("name"), "channel name must be set"))
					}

					if channel.Spec.DestinationName == "" {
						chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("destinationName"), "a destination name must be set to associate channel with"))
					}

					if channel.Spec.Product != "IINT" {
						chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("product"), "product must be set to `IINT`"))
					}

					if channel.Spec.Type == "" {
						chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("type"), "channel type must be set"))
					} else {
						if channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.EMAIL &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.EVENT_BRIDGE &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.JIRA_CLASSIC &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.JIRA_NEXTGEN &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.MOBILE_PUSH &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_ACCOUNT_INTEGRATION &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_SERVICE_INTEGRATION &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SERVICE_NOW_APP &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SERVICENOW_INCIDENTS &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SLACK &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.SLACK_COLLABORATION &&
							channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.WEBHOOK {
							chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("type"), channel.Spec.Type, "invalid channel type - must be one of: EMAIL | EVENT_BRIDGE | JIRA_CLASSIC | JIRA_NEXTGEN | MOBILE_PUSH | PAGERDUTY_SERVICE_INTEGRATION | PAGERDUTY_ACCOUNT_INTEGRATION | SERVICE_NOW_APP | SERVICENOW_INCIDENTS | SLACK | SLACK_COLLABORATION | WEBHOOK"))
						}
					}

					//properties validation
					if channel.Spec.Properties != nil {
						if len(channel.Spec.Properties) > 0 {
							for i, prop := range channel.Spec.Properties {
								if prop.Key == "" && channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.MOBILE_PUSH {
									chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), "at least 1 property key must be set"))
								}

								if prop.Value == "" && channel.Spec.Type != notifications.AiNotificationsChannelTypeTypes.MOBILE_PUSH {
									chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("value"), "at least 1 property value must be set"))
								}

								switch channel.Spec.Type {
								case notifications.AiNotificationsChannelTypeTypes.EMAIL:
									if prop.Key != "subject" && prop.Key != "customDetailsEmail" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be one of `subject` or `customDetailsEmail` for email type channels"))
									}
								case notifications.AiNotificationsChannelTypeTypes.EVENT_BRIDGE:
									if prop.Key != "eventSource" && prop.Key != "eventContent" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "required keys `AWSAccountId` and `AWSRegion` must be specified for event bridge destinations"))
									}
								case notifications.AiNotificationsChannelTypeTypes.JIRA_CLASSIC:
									if prop.Key != "project" && prop.Key != "issueType" && prop.Key != "description" && prop.Key != "summary" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: project | issueType | description | summary"))
									}
								case notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_SERVICE_INTEGRATION:
									if prop.Key != "summary" && prop.Key != "customDetails" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: summary | customDetails"))
									}
								case notifications.AiNotificationsChannelTypeTypes.PAGERDUTY_ACCOUNT_INTEGRATION:
									if prop.Key != "summary" && prop.Key != "service" && prop.Key != "email" && prop.Key != "customDetails" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: summary | service | email | customDetails"))
									}
								case notifications.AiNotificationsChannelTypeTypes.SLACK:
									if prop.Key != "channelId" && prop.Key != "customDetailsSlack" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: channelId | customDetailsSlack"))
									}
								case notifications.AiNotificationsChannelTypeTypes.SLACK_COLLABORATION:
									if prop.Key != "channelId" && prop.Key != "customDetailsSlack" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "Key must be one of: channelId | customDetailsSlack"))
									}
								case notifications.AiNotificationsChannelTypeTypes.WEBHOOK:
									if prop.Key != "payload" {
										chanErrs = append(chanErrs, field.TypeInvalid(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties").Index(i).Child("key"), prop.Key, "key must be `payload` for webhook type destinations"))
									}
								}
							}
						} else {
							chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties"), "properties need at least 1 key-value pair defined"))
						}
					} else {
						chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels").Index(c).Child("spec").Child("properties"), "properties must be set"))
					}
				}
			}
		} else {
			chanErrs = append(chanErrs, field.Required(field.NewPath("spec").Child("channels"), "at least one channel must be configured"))
		}
	}

	return chanErrs
}
