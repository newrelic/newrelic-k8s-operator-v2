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

package controller

import (
	"context"
	"reflect"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/ai"
	"github.com/newrelic/newrelic-client-go/v2/pkg/notifications"
	alertsv1 "github.com/newrelic/newrelic-kubernetes-operator-v2/api/v1"
	"github.com/newrelic/newrelic-kubernetes-operator-v2/interfaces"
)

// AlertDestinationReconciler reconciles a AlertDestination object
type AlertDestinationReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	Alerts      interfaces.NewRelicClientInterface
	AlertClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey      string
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertdestinations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertdestinations/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertdestinations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AlertDestination object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *AlertDestinationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var destination alertsv1.AlertDestination
	err := r.Get(ctx, req.NamespacedName, &destination)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("Destination 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET destination", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	r.Log.Info("Starting destination reconcile")

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(destination)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	//init client
	alertClient, errAlertClient := r.AlertClient(r.apiKey, destination.Spec.Region)
	if errAlertClient != nil {
		r.Log.Error(errAlertClient, "Failed to create Alert Client")
		return ctrl.Result{}, errAlertClient
	}
	r.Alerts = alertClient

	//Handle destination deletion
	deleteFinalizer := "newrelic.destination.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if destination.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&destination, deleteFinalizer) {
			controllerutil.AddFinalizer(&destination, deleteFinalizer)
			if err := r.Update(ctx, &destination); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//destination being deleted
		if controllerutil.ContainsFinalizer(&destination, deleteFinalizer) {
			if err := r.deleteDestination(&destination); err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(&destination, deleteFinalizer)
			if err := r.Update(ctx, &destination); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	//no config changes
	if reflect.DeepEqual(&destination.Spec, destination.Status.AppliedSpec) {
		r.Log.Info("No config changes. Skipping reconcile")
		return ctrl.Result{}, nil
	}

	//Look for existing destination
	existingDestId, err := r.getExistingDestination(&destination) //nolint:golint
	if err != nil {
		r.Log.Error(err, "failed to fetch existing destination")
		return ctrl.Result{}, err
	}

	if existingDestId != "" && destination.Status.DestinationID != "" { //exists, and no previous state
		r.Log.Info("Destination found and previous state exists, checking for config mismatch")

		if !reflect.DeepEqual(&destination.Spec, destination.Status.AppliedSpec) {
			r.Log.Info("Config mismatch found, updating destination", "destinationId", destination.Status.DestinationID)

			err := r.updateDestination(&destination)
			if err != nil {
				return ctrl.Result{}, err
			}

			err = r.Status().Update(ctx, &destination)
			if err != nil {
				r.Log.Error(err, "failed to update AlertDestination status")
				return ctrl.Result{}, err
			}

		} else {
			r.Log.Info("Destination unchanged, no update required")
			return ctrl.Result{}, nil
		}
	} else { //doesn't exist
		if destination.Status.DestinationID != "" && !reflect.DeepEqual(&destination.Spec, destination.Status.AppliedSpec) { //handles name change scenario (to not create a duplicate destination)
			r.Log.Info("updating existing destination",
				"destinationId", destination.Status.DestinationID,
				"destinationName", destination.Status.AppliedSpec.Name,
			)
			err := r.updateDestination(&destination)
			if err != nil {
				return ctrl.Result{}, err
			}

			err = r.Status().Update(ctx, &destination)
			if err != nil {
				r.Log.Error(err, "failed to update AlertDestination status")
				return ctrl.Result{}, err
			}
		} else {
			r.Log.Info("No destination id found and no id set in state, creating new destination", "destinationName", destination.Spec.Name)
			err := r.createDestination(&destination)
			if err != nil {
				return ctrl.Result{}, err
			}

			err = r.Status().Update(ctx, &destination)
			if err != nil {
				r.Log.Error(err, "failed to update AlertDestination status")
				return ctrl.Result{}, err
			}
		}
	}

	//update status
	err = r.Status().Update(ctx, &destination)
	if err != nil {
		r.Log.Error(err, "failed to update AlertDestination status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AlertDestinationReconciler) createDestination(destination *alertsv1.AlertDestination) error {
	r.Log.Info("Creating destination", "destinationName", destination.Spec.Name)

	destInput := r.translateDestinationCreateInput(destination)

	createdDestination, err := r.Alerts.Notifications().AiNotificationsCreateDestination(destination.Spec.AccountID, destInput)
	if err != nil {
		r.Log.Error(err, "failed to create destination",
			"destinationName", destination.Spec.Name,
			"accountId", destination.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("createdDestination", "destination", createdDestination)

	destination.Status.AppliedSpec = &destination.Spec
	destination.Status.DestinationID = createdDestination.Destination.ID

	return nil
}

func (r *AlertDestinationReconciler) updateDestination(destination *alertsv1.AlertDestination) error {
	r.Log.Info("Updating destination", "destinationName", destination.Status.AppliedSpec.Name)

	destInput := r.translateDestinationUpdateInput(destination)

	updatedDestination, err := r.Alerts.Notifications().AiNotificationsUpdateDestination(destination.Spec.AccountID, destInput, destination.Status.DestinationID)
	if err != nil {
		r.Log.Error(err, "failed to update destination",
			"destinationName", destination.Spec.Name,
			"destinationID", destination.Status.DestinationID,
			"accountId", destination.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("updatedDestination", "destination", updatedDestination)

	destination.Status.AppliedSpec = &destination.Spec
	destination.Status.DestinationID = updatedDestination.Destination.ID

	return nil
}

func (r *AlertDestinationReconciler) deleteDestination(destination *alertsv1.AlertDestination) error {
	r.Log.Info("Deleting destination", "destinationId", destination.Status.DestinationID)

	_, err := r.Alerts.Notifications().AiNotificationsDeleteDestination(destination.Status.AppliedSpec.AccountID, destination.Status.DestinationID)
	if err != nil {
		r.Log.Error(err, "failed to delete destination",
			"destinationId", destination.Status.DestinationID,
			"accountId", destination.Status.AppliedSpec.AccountID,
			"destinationName", destination.Status.AppliedSpec.Name,
		)
		return err
	}

	return nil
}

func (r *AlertDestinationReconciler) translateDestinationCreateInput(destination *alertsv1.AlertDestination) notifications.AiNotificationsDestinationInput {
	destinationInput := notifications.AiNotificationsDestinationInput{}

	destinationInput.Name = destination.Spec.Name
	destinationInput.Type = destination.Spec.Type
	if destination.Spec.Properties != nil {
		for _, prop := range destination.Spec.Properties {
			props := notifications.AiNotificationsPropertyInput{}
			props.DisplayValue = prop.DisplayValue
			props.Key = prop.Key
			props.Label = prop.Label
			props.Value = prop.Value

			destinationInput.Properties = append(destinationInput.Properties, props)
		}
	} else {
		r.Log.Info("Missing required properties field")
	}

	if destination.Spec.SecureURL != nil {
		destinationInput.SecureURL = &notifications.AiNotificationsSecureURLInput{}
		destinationInput.SecureURL.Prefix = destination.Spec.SecureURL.Prefix
		destinationInput.SecureURL.SecureSuffix = destination.Spec.SecureURL.SecureSuffix
	}

	if destination.Spec.Auth != nil {
		destinationInput.Auth = &notifications.AiNotificationsCredentialsInput{}

		switch destination.Spec.Auth.Type {
		case "BASIC":
			destinationInput.Auth.Type = "BASIC"
			destinationInput.Auth.Basic = notifications.AiNotificationsBasicAuthInput{}
			destinationInput.Auth.Basic.User = destination.Spec.Auth.Basic.User
			destinationInput.Auth.Basic.Password = destination.Spec.Auth.Basic.Password
		case "CUSTOM_HEADERS":
			destinationInput.Auth.Type = "CUSTOM_HEADERS"
			destinationInput.Auth.CustomHeaders = &notifications.AiNotificationsCustomHeadersAuthInput{}
			destinationInput.Auth.CustomHeaders.CustomHeaders = destination.Spec.Auth.CustomHeaders.CustomHeaders

		case "OAUTH2":
			destinationInput.Auth.Type = "OAUTH2"
			destinationInput.Auth.Oauth2 = notifications.AiNotificationsOAuth2AuthInput{}
			destinationInput.Auth.Oauth2 = destination.Spec.Auth.Oauth2

		case "TOKEN":
			destinationInput.Auth.Type = "TOKEN"
			destinationInput.Auth.Token.Prefix = destination.Spec.Auth.Token.Prefix
			destinationInput.Auth.Token.Token = destination.Spec.Auth.Token.Token
		}
	}

	return destinationInput
}

func (r *AlertDestinationReconciler) translateDestinationUpdateInput(destination *alertsv1.AlertDestination) notifications.AiNotificationsDestinationUpdate {
	destinationInput := notifications.AiNotificationsDestinationUpdate{}

	destinationInput.Name = destination.Spec.Name
	destinationInput.Active = destination.Spec.Active
	if destination.Spec.Properties != nil {
		for _, prop := range destination.Spec.Properties {
			props := notifications.AiNotificationsPropertyInput{}
			props.DisplayValue = prop.DisplayValue
			props.Key = prop.Key
			props.Label = prop.Label
			props.Value = prop.Value

			destinationInput.Properties = append(destinationInput.Properties, props)
		}
	} else {
		r.Log.Info("Missing required properties field")
	}

	if destination.Spec.SecureURL != nil {
		destinationInput.SecureURL = &notifications.AiNotificationsSecureURLUpdate{}
		destinationInput.SecureURL.Prefix = destination.Spec.SecureURL.Prefix
		destinationInput.SecureURL.SecureSuffix = destination.Spec.SecureURL.SecureSuffix
	}

	if destination.Spec.Auth != nil {
		destinationInput.Auth = &notifications.AiNotificationsCredentialsInput{}
		switch destination.Spec.Auth.Type {
		case "BASIC":
			destinationInput.Auth.Type = "BASIC"
			destinationInput.Auth.Basic = notifications.AiNotificationsBasicAuthInput{}
			destinationInput.Auth.Basic.User = destination.Spec.Auth.Basic.User
			destinationInput.Auth.Basic.Password = destination.Spec.Auth.Basic.Password
		case "CUSTOM_HEADERS":
			destinationInput.Auth.Type = "CUSTOM_HEADERS"
			destinationInput.Auth.CustomHeaders = &notifications.AiNotificationsCustomHeadersAuthInput{}
			destinationInput.Auth.CustomHeaders.CustomHeaders = destination.Spec.Auth.CustomHeaders.CustomHeaders
		case "OAUTH2":
			destinationInput.Auth.Type = "OAUTH2"
			destinationInput.Auth.Oauth2 = notifications.AiNotificationsOAuth2AuthInput{}
			destinationInput.Auth.Oauth2 = destination.Spec.Auth.Oauth2

		case "TOKEN":
			destinationInput.Auth.Type = "TOKEN"
			destinationInput.Auth.Token.Prefix = destination.Spec.Auth.Token.Prefix
			destinationInput.Auth.Token.Token = destination.Spec.Auth.Token.Token
		}
	}

	return destinationInput
}

// getExistingDestination fetches existing destinations
func (r *AlertDestinationReconciler) getExistingDestination(destination *alertsv1.AlertDestination) (string, error) {
	r.Log.Info("Checking for existing destination", "destinationName", destination.Spec.Name)

	searchParams := ai.AiNotificationsDestinationFilter{
		Name: destination.Spec.Name,
	}

	sorter := notifications.AiNotificationsDestinationSorter{}

	existingDests, err := r.Alerts.Notifications().GetDestinations(destination.Spec.AccountID, "", searchParams, sorter)
	if err != nil {
		r.Log.Error(err, "failed to get list of destinations",
			"destinationName", destination.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return "", err
	}

	if len(existingDests.Entities) > 0 {
		for _, existingDest := range existingDests.Entities {
			if existingDest.ID == destination.Status.DestinationID {
				r.Log.Info("Found destination, returning")
				destination.Status.DestinationID = existingDest.ID
				destination.Spec.ID = existingDest.ID
				break
			}
		}

		return destination.Spec.ID, nil
	}

	return "", nil
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *AlertDestinationReconciler) getAPIKeyOrSecret(destination alertsv1.AlertDestination) (string, error) {
	if destination.Spec.APIKey != "" {
		return destination.Spec.APIKey, nil
	}

	if destination.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: destination.Spec.APIKeySecret.Namespace, Name: destination.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[destination.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}
	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AlertDestinationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.AlertDestination{}).
		Complete(r)
}
