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
	"github.com/newrelic/newrelic-client-go/v2/pkg/workflows"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// AlertWorkflowSpec defines the desired state of AlertWorkflow
type AlertWorkflowSpec struct {
	APIKey              string                                   `json:"apiKey,omitempty"`
	APIKeySecret        NewRelicSecret                           `json:"apiKeySecret,omitempty"`
	AccountID           int                                      `json:"accountId,omitempty"`
	Region              string                                   `json:"region,omitempty"`
	Name                string                                   `json:"name"`
	Enabled             bool                                     `json:"enabled,omitempty"`
	IssuesFilter        *AiWorkflowsFilterInput                  `json:"issuesFilter,omitempty"`
	Enrichments         AiWorkflowsEnrichmentsInput              `json:"enrichments,omitempty"`
	EnrichmentsEnabled  bool                                     `json:"enrichmentsEnabled,omitempty"`
	MutingRulesHandling workflows.AiWorkflowsMutingRulesHandling `json:"mutingRulesHandling"`
	Channels            []DestinationChannel                     `json:"channels"`
	ChannelsEnabled     bool                                     `json:"channelsEnabled,omitempty"`
}

// DestinationChannel defines the channels attached to a workflow
type DestinationChannel struct {
	ChannelID             string                                     `json:"channelId,omitempty"`
	NotificationTriggers  []workflows.AiWorkflowsNotificationTrigger `json:"notificationTriggers"`
	UpdateOriginalMessage *bool                                      `json:"updateOriginalMessage,omitempty"` // Update original notification message (Slack channels only)
	Spec                  DestinationChannelSpec                     `json:"spec,omitempty"`
}

// DestinationChannelSpec defines the desired state of a DestinationChannel
type DestinationChannelSpec struct {
	AlertChannelSpec `json:",inline"`
}

// // AiWorkflowsDestinationConfiguration defines the destination channel config for a workflow
// type AiWorkflowsDestinationConfiguration struct {
// 	ChannelID            string                                     `json:"channelId"`
// 	Name                 string                                     `json:"name,omitempty"`
// 	NotificationTriggers []workflows.AiWorkflowsNotificationTrigger `json:"notificationTriggers"`
// 	Type                 workflows.AiWorkflowsDestinationType       `json:"type,omitempty"`
// 	// Update original notification message (Slack channels only)
// 	UpdateOriginalMessage *bool `json:"updateOriginalMessage,omitempty"`
// }

// AiWorkflowsFilterInput defines the filters for a workflow
type AiWorkflowsFilterInput struct {
	Name       string                          `json:"name,omitempty"`
	Predicates []AiWorkflowsPredicateInput     `json:"predicates,omitempty"`
	Type       workflows.AiWorkflowsFilterType `json:"type"`
}

// AiWorkflowsPredicateInput - PredicateInput input object
type AiWorkflowsPredicateInput struct {
	Attribute string                        `json:"attribute"`
	Operator  workflows.AiWorkflowsOperator `json:"operator"`
	Values    []string                      `json:"values"`
}

// AiWorkflowsEnrichmentsInput defines optional enrichment for a workflow
type AiWorkflowsEnrichmentsInput struct {
	NRQL []AiWorkflowsNRQLEnrichmentInput `json:"nrql,omitempty"`
}

// AiWorkflowsNRQLEnrichmentInput - NRQL type enrichment input object
type AiWorkflowsNRQLEnrichmentInput struct {
	Configuration []AiWorkflowsNRQLConfigurationInput `json:"configuration,omitempty"`
	Name          string                              `json:"name"`
}

// AiWorkflowsNRQLConfigurationInput - NRQL type configuration input object
type AiWorkflowsNRQLConfigurationInput struct {
	Query string `json:"query"`
}

// AlertWorkflowStatus defines the observed state of AlertWorkflow
type AlertWorkflowStatus struct {
	AppliedSpec *AlertWorkflowSpec `json:"appliedSpec"`
	WorkflowID  string             `json:"workflowId"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AlertWorkflow is the Schema for the alertworkflows API
type AlertWorkflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlertWorkflowSpec   `json:"spec,omitempty"`
	Status AlertWorkflowStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AlertWorkflowList contains a list of AlertWorkflow
type AlertWorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlertWorkflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlertWorkflow{}, &AlertWorkflowList{})
}
