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
	"github.com/newrelic/newrelic-client-go/v2/pkg/servicelevel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ServiceLevelSpec defines the desired state of ServiceLevel
type ServiceLevelSpec struct {
	APIKey       string         `json:"apiKey,omitempty"`
	APIKeySecret NewRelicSecret `json:"apiKeySecret,omitempty"`
	Region       string         `json:"region,omitempty"`
	AccountID    int            `json:"accountId,omitempty"`
	// EntityGUID is the entity (APM service, browser app, workload, ...) the SLI
	// attaches to. Required and immutable.
	EntityGUID common.EntityGUID `json:"entityGuid"`

	ServiceLevelIndicatorSpec `json:",inline"`
}

// ServiceLevelIndicatorSpec is the subset of ServiceLevelSpec that round-trips
// through the New Relic API. This is the ONLY thing compared against remote state.
type ServiceLevelIndicatorSpec struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Events      ServiceLevelEvents      `json:"events"`
	Objectives  []ServiceLevelObjective `json:"objectives"`
}

// ServiceLevelEvents defines the events that make up the SLI
type ServiceLevelEvents struct {
	ValidEvents *ServiceLevelEventsQuery `json:"validEvents,omitempty"`
	GoodEvents  *ServiceLevelEventsQuery `json:"goodEvents,omitempty"`
	BadEvents   *ServiceLevelEventsQuery `json:"badEvents,omitempty"`
}

// ServiceLevelEventsQuery defines a single events query (validEvents/goodEvents/badEvents)
type ServiceLevelEventsQuery struct {
	From   servicelevel.NRQL              `json:"from"`
	Where  servicelevel.NRQL              `json:"where,omitempty"`
	Select *ServiceLevelEventsQuerySelect `json:"select,omitempty"`
}

// ServiceLevelEventsQuerySelect defines the SELECT clause used to aggregate events
type ServiceLevelEventsQuerySelect struct {
	Function  servicelevel.ServiceLevelEventsQuerySelectFunction `json:"function,omitempty"`
	Attribute string                                             `json:"attribute,omitempty"`
	Threshold string                                             `json:"threshold,omitempty"` // decimal-as-string
}

// ServiceLevelObjective defines a single SLO target for the SLI
type ServiceLevelObjective struct {
	Name        string                          `json:"name,omitempty"`
	Description string                          `json:"description,omitempty"`
	Target      string                          `json:"target"` // decimal-as-string, e.g. "99.9"
	TimeWindow  ServiceLevelObjectiveTimeWindow `json:"timeWindow"`
}

// ServiceLevelObjectiveTimeWindow defines the time window configuration of the SLO
type ServiceLevelObjectiveTimeWindow struct {
	Rolling ServiceLevelObjectiveRollingTimeWindow `json:"rolling,omitempty"`
}

// ServiceLevelObjectiveRollingTimeWindow defines the rolling time window configuration of the SLO
type ServiceLevelObjectiveRollingTimeWindow struct {
	Count int                                                     `json:"count"`
	Unit  servicelevel.ServiceLevelObjectiveRollingTimeWindowUnit `json:"unit,omitempty"`
}

// ServiceLevelStatus defines the observed state of ServiceLevel
type ServiceLevelStatus struct {
	AppliedSpec      *ServiceLevelSpec `json:"appliedSpec"`
	ServiceLevelGUID common.EntityGUID `json:"serviceLevelGuid,omitempty"` // the SLI's OWN guid
	ServiceLevelID   string            `json:"serviceLevelId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Created",type="boolean",JSONPath=".status.created"

// ServiceLevel is the Schema for the servicelevels API
type ServiceLevel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceLevelSpec   `json:"spec,omitempty"`
	Status ServiceLevelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceLevelList contains a list of ServiceLevel
type ServiceLevelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceLevel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceLevel{}, &ServiceLevelList{})
}
