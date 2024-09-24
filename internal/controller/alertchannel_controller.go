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

// AlertChannelReconciler reconciles a AlertChannel object
type AlertChannelReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	Alerts      interfaces.NewRelicClientInterface
	AlertClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey      string
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertchannels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertchannels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertchannels/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AlertChannel object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *AlertChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var channel alertsv1.AlertChannel
	err := r.Get(ctx, req.NamespacedName, &channel)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("Channel 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET channel", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	r.Log.Info("Starting channel reconcile")

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(channel)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	//init client
	alertClient, errAlertClient := r.AlertClient(r.apiKey, channel.Spec.Region)
	if errAlertClient != nil {
		r.Log.Error(errAlertClient, "Failed to create Alert Client")
		return ctrl.Result{}, errAlertClient
	}
	r.Alerts = alertClient

	//Handle channel deletion
	deleteFinalizer := "newrelic.channel.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if channel.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&channel, deleteFinalizer) {
			controllerutil.AddFinalizer(&channel, deleteFinalizer)
			if err := r.Update(ctx, &channel); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//channel being deleted
		if controllerutil.ContainsFinalizer(&channel, deleteFinalizer) {
			if err := r.deleteChannel(&channel); err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(&channel, deleteFinalizer)
			if err := r.Update(ctx, &channel); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	//no config changes
	if reflect.DeepEqual(&channel.Spec, channel.Status.AppliedSpec) {
		r.Log.Info("No config changes. Skipping reconcile")
		return ctrl.Result{}, nil
	}

	if channel.Status.ChannelID != "" { //can only fetch channels with ID - so if no state at all (first run) - create a channel
		existingChannelId, err := r.getExistingChannel(&channel) //nolint:golint
		if err != nil {
			r.Log.Error(err, "failed to fetch existing channel")
			return ctrl.Result{}, err
		}

		if existingChannelId != "" { //exists, and no previous state
			r.Log.Info("Channel found and previous state exists, checking for config mismatch")

			if !reflect.DeepEqual(&channel.Spec, channel.Status.AppliedSpec) {
				r.Log.Info("Config mismatch found, updating channel", "channelId", channel.Status.ChannelID)

				err := r.updateChannel(&channel)
				if err != nil {
					return ctrl.Result{}, err
				}

				err = r.Status().Update(ctx, &channel)
				if err != nil {
					r.Log.Error(err, "failed to update AlertChannel status")
					return ctrl.Result{}, err
				}

			} else {
				r.Log.Info("Channel unchanged, no update required")
				return ctrl.Result{}, nil
			}
		} else { //doesn't exist
			if channel.Status.ChannelID != "" && !reflect.DeepEqual(&channel.Spec, channel.Status.AppliedSpec) { //handles name change scenario (to not create a duplicate channel)
				r.Log.Info("updating existing channel",
					"channelId", channel.Status.ChannelID,
					"channelName", channel.Status.AppliedSpec.Name,
				)
				err := r.updateChannel(&channel)
				if err != nil {
					return ctrl.Result{}, err
				}

				err = r.Status().Update(ctx, &channel)
				if err != nil {
					r.Log.Error(err, "failed to update AlertChannel status")
					return ctrl.Result{}, err
				}
			} else {
				r.Log.Info("No channel id found and no id set in state, creating new channel", "channelName", channel.Spec.Name)
				err := r.createChannel(&channel)
				if err != nil {
					return ctrl.Result{}, err
				}

				err = r.Status().Update(ctx, &channel)
				if err != nil {
					r.Log.Error(err, "failed to update AlertChannel status")
					return ctrl.Result{}, err
				}
			}
		}
	} else {
		r.Log.Info("No channel id set in state, creating new channel", "channelName", channel.Spec.Name)
		err := r.createChannel(&channel)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Status().Update(ctx, &channel)
		if err != nil {
			r.Log.Error(err, "failed to update AlertChannel status")
			return ctrl.Result{}, err
		}
	}

	//update status
	err = r.Status().Update(ctx, &channel)
	if err != nil {
		r.Log.Error(err, "failed to update AlertChannel status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AlertChannelReconciler) getExistingChannel(channel *alertsv1.AlertChannel) (string, error) {
	r.Log.Info("Checking for existing channel", "channelName", channel.Spec.Name)

	searchParams := ai.AiNotificationsChannelFilter{
		ID: channel.Status.ChannelID,
	}

	sorter := notifications.AiNotificationsChannelSorter{}

	existingChannels, err := r.Alerts.Notifications().GetChannels(channel.Spec.AccountID, "", searchParams, sorter)
	if err != nil {
		r.Log.Error(err, "failed to get channel",
			"channelName", channel.Spec.Name,
			"channelId", channel.Status.ChannelID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
	}

	if len(existingChannels.Entities) > 0 {
		for _, existingChannel := range existingChannels.Entities {
			if existingChannel.ID == channel.Status.ChannelID {
				r.Log.Info("Found channel, returning")
				channel.Status.ChannelID = existingChannel.ID
				break
			}
		}

		return channel.Status.ChannelID, nil
	}

	return "", nil
}

func (r *AlertChannelReconciler) createChannel(channel *alertsv1.AlertChannel) error {
	r.Log.Info("Creating channel", "channelName", channel.Spec.Name)

	chanInput := r.translateChannelCreateInput(channel)

	createdChannel, err := r.Alerts.Notifications().AiNotificationsCreateChannel(channel.Spec.AccountID, chanInput)
	if err != nil {
		r.Log.Error(err, "failed to create channel",
			"destinationId", channel.Spec.DestinationID,
			"accountId", channel.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	// r.Log.Info("createdChannel", "channel", createdChannel)

	channel.Status.AppliedSpec = &channel.Spec
	channel.Status.ChannelID = createdChannel.Channel.ID

	return nil
}

func (r *AlertChannelReconciler) updateChannel(channel *alertsv1.AlertChannel) error {
	r.Log.Info("Updating channel", "channelName", channel.Spec.Name)

	chanInput := r.translateChannelUpdateInput(channel)

	updatedChannel, err := r.Alerts.Notifications().AiNotificationsUpdateChannel(channel.Spec.AccountID, chanInput, channel.Status.ChannelID)
	if err != nil {
		r.Log.Error(err, "failed to updated channel",
			"channelId", channel.Status.ChannelID,
			"accountId", channel.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	// r.Log.Info("updatedChannel", "channel", updatedChannel)

	channel.Status.AppliedSpec = &channel.Spec
	channel.Status.ChannelID = updatedChannel.Channel.ID

	return nil
}

func (r *AlertChannelReconciler) deleteChannel(channel *alertsv1.AlertChannel) error {
	r.Log.Info("Deleting channel", "channelId", channel.Status.ChannelID)

	_, err := r.Alerts.Notifications().AiNotificationsDeleteChannel(channel.Spec.AccountID, channel.Status.ChannelID)
	if err != nil {
		r.Log.Error(err, "failed to delete channel",
			"channelId", channel.Status.ChannelID,
			"accountId", channel.Status.AppliedSpec.AccountID,
			"channelName", channel.Status.AppliedSpec.Name,
		)
		return err
	}

	return nil
}

func (r *AlertChannelReconciler) translateChannelCreateInput(channel *alertsv1.AlertChannel) notifications.AiNotificationsChannelInput {
	channelInput := notifications.AiNotificationsChannelInput{}
	channelInput.Name = channel.Spec.Name
	channelInput.Type = channel.Spec.Type
	channelInput.Product = channel.Spec.Product

	if channel.Spec.Properties != nil {
		for _, prop := range channel.Spec.Properties {
			props := notifications.AiNotificationsPropertyInput{}
			props.DisplayValue = prop.DisplayValue
			props.Key = prop.Key
			props.Label = prop.Label
			props.Value = prop.Value

			channelInput.Properties = append(channelInput.Properties, props)
		}
	} else {
		r.Log.Info("Missing required properties field")
	}

	channelInput.DestinationId = channel.Spec.DestinationID

	return channelInput
}

func (r *AlertChannelReconciler) translateChannelUpdateInput(channel *alertsv1.AlertChannel) notifications.AiNotificationsChannelUpdate {
	channelInput := notifications.AiNotificationsChannelUpdate{}
	channelInput.Name = channel.Spec.Name
	channelInput.Active = channel.Spec.Active //idk what this field actually does

	if channel.Spec.Properties != nil {
		for _, prop := range channel.Spec.Properties {
			props := notifications.AiNotificationsPropertyInput{}
			props.DisplayValue = prop.DisplayValue
			props.Key = prop.Key
			props.Label = prop.Label
			props.Value = prop.Value

			channelInput.Properties = append(channelInput.Properties, props)
		}
	} else {
		r.Log.Info("Missing required properties field")
	}

	return channelInput
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *AlertChannelReconciler) getAPIKeyOrSecret(channel alertsv1.AlertChannel) (string, error) {
	if channel.Spec.APIKey != "" {
		return channel.Spec.APIKey, nil
	}

	if channel.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: channel.Spec.APIKeySecret.Namespace, Name: channel.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[channel.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}
	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AlertChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.AlertChannel{}).
		Complete(r)
}
