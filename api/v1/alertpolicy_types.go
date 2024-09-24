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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Allowed incident preferences
const (
	IncidentPreferencePerPolicy             = "PER_POLICY"
	IncidentPreferencePerCondition          = "PER_CONDITION"
	IncidentPreferencePerConditionAndTarget = "PER_CONDITION_AND_TARGET"
)

// AlertPolicySpec defines the desired state of AlertPolicy
type AlertPolicySpec struct {
	// +kubebuilder:validation:Enum=PER_POLICY;PER_CONDITION;PER_CONDITION_AND_TARGET
	IncidentPreference string            `json:"incidentPreference,omitempty"`
	Name               string            `json:"name"`
	APIKey             string            `json:"apiKey,omitempty"`
	APIKeySecret       NewRelicSecret    `json:"apiKeySecret,omitempty"`
	Region             string            `json:"region"`
	AccountID          int               `json:"accountId,omitempty"`
	Conditions         []PolicyCondition `json:"conditions,omitempty"`
}

// PolicyCondition defines the conditions contained within an AlertPolicy
type PolicyCondition struct {
	Name string              `json:"name,omitempty"`
	ID   string              `json:"id,omitempty"`
	Spec PolicyConditionSpec `json:"spec,omitempty"`
}

// PolicyConditionSpec defines the desired state of PolicyCondition
type PolicyConditionSpec struct {
	NrqlConditionSpec `json:",inline"`
}

// AlertPolicyStatus defines the observed state of AlertPolicy
type AlertPolicyStatus struct {
	AppliedSpec *AlertPolicySpec `json:"appliedSpec"`
	PolicyID    int              `json:"policyId"`
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AlertPolicy is the Schema for the alertpolicies API
type AlertPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlertPolicySpec   `json:"spec,omitempty"`
	Status AlertPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AlertPolicyList contains a list of AlertPolicy
type AlertPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlertPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlertPolicy{}, &AlertPolicyList{})
}
