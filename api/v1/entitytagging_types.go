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
	"github.com/newrelic/newrelic-client-go/v2/pkg/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// EntityTaggingSpec defines the desired state of EntityTagging
type EntityTaggingSpec struct {
	TagBaseSpec `json:",inline"`
}

type TagBaseSpec struct {
	APIKey       string         `json:"apiKey,omitempty"`
	APIKeySecret NewRelicSecret `json:"apiKeySecret,omitempty"`
	// AccountID    int            `json:"accountId,omitempty"`
	Region      string       `json:"region,omitempty"`
	EntityNames []string     `json:"entityNames"`
	Tags        []*EntityTag `json:"tags"`
}

// EntityTag represents a single key-value tag to attach to a condition
type EntityTag struct {
	Key    string   `json:"key,omitempty"`
	Values []string `json:"values,omitempty"`
}

// ManagedEntity represents an entity name-guid pair stored in local status
type ManagedEntity struct {
	Name string            `json:"name"`
	GUID common.EntityGUID `json:"guid"`
}

// EntityTaggingStatus defines the observed state of EntityTagging
type EntityTaggingStatus struct {
	AppliedSpec     *EntityTaggingSpec `json:"appliedSpec"`
	ManagedEntities []ManagedEntity    `json:"managedEntities"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Created",type="boolean",JSONPath=".status.created"

// EntityTagging is the Schema for the entitytaggings API
type EntityTagging struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EntityTaggingSpec   `json:"spec,omitempty"`
	Status EntityTaggingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EntityTaggingList contains a list of EntityTagging
type EntityTaggingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EntityTagging `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EntityTagging{}, &EntityTaggingList{})
}
