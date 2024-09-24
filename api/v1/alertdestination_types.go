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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// AlertDestinationSpec defines the desired state of AlertDestination
type AlertDestinationSpec struct {
	AlertDestinationBaseSpec `json:",inline"`
}

// AlertDestinationBaseSpec defines the desired state when creating a destination
type AlertDestinationBaseSpec struct {
	APIKey       string                                       `json:"apiKey,omitempty"`
	APIKeySecret NewRelicSecret                               `json:"apiKeySecret,omitempty"`
	AccountID    int                                          `json:"accountId,omitempty"`
	Region       string                                       `json:"region,omitempty"`
	Auth         *AiNotificationsCredentialsInput             `json:"auth,omitempty"`
	Name         string                                       `json:"name"`
	ID           string                                       `json:"id,omitempty"`
	Active       bool                                         `json:"active,omitempty"`
	Properties   []AiNotificationsPropertyInput               `json:"properties"`
	SecureURL    *notifications.AiNotificationsSecureURLInput `json:"secureUrl,omitempty"`
	Type         notifications.AiNotificationsDestinationType `json:"type"`
}

// AiNotificationsPropertyInput defines the inputs for a given destination type
type AiNotificationsPropertyInput struct {
	DisplayValue string `json:"displayValue,omitempty"`
	Key          string `json:"key"`
	Label        string `json:"label,omitempty"`
	Value        string `json:"value"`
}

// AiNotificationsCredentialsInput - Credential input object
type AiNotificationsCredentialsInput struct {
	Basic         notifications.AiNotificationsBasicAuthInput  `json:"basic,omitempty"`
	CustomHeaders *AiNotificationsCustomHeadersAuthInput       `json:"customHeaders,omitempty"`
	Oauth2        notifications.AiNotificationsOAuth2AuthInput `json:"oauth2,omitempty"`
	Token         notifications.AiNotificationsTokenAuthInput  `json:"token,omitempty"`
	Type          notifications.AiNotificationsAuthType        `json:"type"`
}

// AiNotificationsCustomHeadersAuthInput - Custom headers auth input object
type AiNotificationsCustomHeadersAuthInput struct {
	CustomHeaders []notifications.AiNotificationsCustomHeaderInput `json:"customHeaders,omitempty"`
}

// AlertDestinationStatus defines the observed state of AlertDestination
type AlertDestinationStatus struct {
	AppliedSpec   *AlertDestinationSpec `json:"appliedSpec"`
	DestinationID string                `json:"destinationId"`
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AlertDestination is the Schema for the alertdestinations API
type AlertDestination struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlertDestinationSpec   `json:"spec,omitempty"`
	Status AlertDestinationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AlertDestinationList contains a list of AlertDestination
type AlertDestinationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlertDestination `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlertDestination{}, &AlertDestinationList{})
}
