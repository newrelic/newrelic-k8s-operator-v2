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
	"github.com/newrelic/newrelic-client-go/v2/pkg/alerts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NrqlConditionSpec defines the desired state of NrqlCondition
type NrqlConditionSpec struct {
	AlertsNrqlBaseSpec         `json:",inline"`
	AlertsBaselineSpecificSpec `json:",inline"`
}

// AlertsNrqlBaseSpec defines the desired state of AlertsNrqlCondition
type AlertsNrqlBaseSpec struct {
	Description               string                                 `json:"description,omitempty"`
	APIKey                    string                                 `json:"apiKey,omitempty"`
	APIKeySecret              NewRelicSecret                         `json:"apiKeySecret,omitempty"`
	AccountID                 int                                    `json:"accountId,omitempty"`
	ExistingPolicyID          string                                 `json:"existingPolicyId,omitempty"`
	ID                        int                                    `json:"id,omitempty"`
	Enabled                   bool                                   `json:"enabled,omitempty"`
	Region                    string                                 `json:"region,omitempty"`
	Name                      string                                 `json:"name,omitempty"`
	Nrql                      NrqlConditionCreateQuery               `json:"nrql,omitempty"`
	RunbookURL                string                                 `json:"runbookUrl,omitempty"`
	Terms                     []AlertsNrqlConditionTerm              `json:"terms,omitempty"`
	Type                      alerts.NrqlConditionType               `json:"type,omitempty"`
	ViolationTimeLimit        alerts.NrqlConditionViolationTimeLimit `json:"violationTimeLimit,omitempty"`
	ViolationTimeLimitSeconds int                                    `json:"violationTimeLimitSeconds,omitempty"`
	Expiration                *AlertsNrqlConditionExpiration         `json:"expiration,omitempty"`
	Signal                    *AlertsNrqlConditionSignal             `json:"signal,omitempty"`
	TitleTemplate             *string                                `json:"titleTemplate,omitempty"`
}

// NrqlConditionCreateQuery defines a nrql configuration
type NrqlConditionCreateQuery struct {
	Query            string `json:"query,omitempty"`
	DataAccountID    *int   `json:"dataAccountId,omitempty"`
	EvaluationOffset *int   `json:"evaluationOffset,omitempty"`
}

// AlertsNrqlConditionSignal - Configuration that defines the signal that the NRQL condition will use to evaluate.
type AlertsNrqlConditionSignal struct {
	AggregationWindow *int                                   `json:"aggregationWindow,omitempty"`
	EvaluationOffset  *int                                   `json:"evaluationOffset,omitempty"`
	EvaluationDelay   *int                                   `json:"evaluationDelay,omitempty"`
	FillOption        *alerts.AlertsFillOption               `json:"fillOption,omitempty"`
	FillValue         *float64                               `json:"fillValue,omitempty"`
	AggregationMethod *alerts.NrqlConditionAggregationMethod `json:"aggregationMethod,omitempty"`
	AggregationDelay  *int                                   `json:"aggregationDelay,omitempty"`
	AggregationTimer  *int                                   `json:"aggregationTimer,omitempty"`
	SlideBy           *int                                   `json:"slideBy,omitempty"`
}

// AlertsNrqlConditionExpiration - Settings for how violations are opened or closed when a signal expires.
type AlertsNrqlConditionExpiration struct {
	ExpirationDuration          *int `json:"expirationDuration,omitempty"`
	CloseViolationsOnExpiration bool `json:"closeViolationsOnExpiration,omitempty"`
	OpenViolationOnExpiration   bool `json:"openViolationOnExpiration,omitempty"`
	IgnoreOnExpectedTermination bool `json:"ignoreOnExpectedTermination,omitempty"`
}

// AlertsNrqlConditionTerm represents the terms of a New Relic alert condition.
type AlertsNrqlConditionTerm struct {
	Operator             alerts.AlertsNRQLConditionTermsOperator `json:"operator,omitempty"`
	Priority             alerts.NrqlConditionPriority            `json:"priority,omitempty"`
	Threshold            string                                  `json:"threshold,omitempty"`
	ThresholdDuration    int                                     `json:"thresholdDuration,omitempty"`
	ThresholdOccurrences alerts.ThresholdOccurrence              `json:"thresholdOccurrences,omitempty"`
}

// AlertsBaselineSpecificSpec - Settings for the direction of anomaly type alerts
type AlertsBaselineSpecificSpec struct {
	BaselineDirection *alerts.NrqlBaselineDirection `json:"baselineDirection,omitempty"`
}

// NrqlConditionStatus defines the observed state of NrqlCondition
type NrqlConditionStatus struct {
	AppliedSpec *NrqlConditionSpec `json:"appliedSpec"`
	ConditionID string             `json:"conditionId"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Created",type="boolean",JSONPath=".status.created"

// NrqlCondition is the Schema for the nrqlconditions API
type NrqlCondition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NrqlConditionSpec   `json:"spec,omitempty"`
	Status NrqlConditionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NrqlConditionList contains a list of NrqlCondition
type NrqlConditionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NrqlCondition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NrqlCondition{}, &NrqlConditionList{})
}
