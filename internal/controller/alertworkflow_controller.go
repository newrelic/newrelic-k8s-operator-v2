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
	"errors"
	"reflect"
	"strings"

	v1 "k8s.io/api/core/v1"
	kErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/ai"
	"github.com/newrelic/newrelic-client-go/v2/pkg/notifications"
	"github.com/newrelic/newrelic-client-go/v2/pkg/workflows"
	alertsv1 "github.com/newrelic/newrelic-kubernetes-operator-v2/api/v1"
	"github.com/newrelic/newrelic-kubernetes-operator-v2/interfaces"
)

// AlertWorkflowReconciler reconciles a AlertWorkflow object
type AlertWorkflowReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	Alerts      interfaces.NewRelicClientInterface
	AlertClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey      string
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertworkflows,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertworkflows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=alertworkflows/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AlertWorkflow object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *AlertWorkflowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var workflow alertsv1.AlertWorkflow
	err := r.Get(ctx, req.NamespacedName, &workflow)
	if err != nil {
		if kErr.IsNotFound(err) {
			r.Log.Info("Workflow 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET workflow", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	r.Log.Info("Starting worfklow reconcile")

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(workflow)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	//init client
	alertClient, errAlertClient := r.AlertClient(r.apiKey, workflow.Spec.Region)
	if errAlertClient != nil {
		r.Log.Error(errAlertClient, "Failed to create Alert Client")
		return ctrl.Result{}, errAlertClient
	}
	r.Alerts = alertClient

	//Handle policy deletion
	deleteFinalizer := "newrelic.workflows.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if workflow.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&workflow, deleteFinalizer) {
			controllerutil.AddFinalizer(&workflow, deleteFinalizer)
			if err := r.Update(ctx, &workflow); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//Workflow is being deleted
		if controllerutil.ContainsFinalizer(&workflow, deleteFinalizer) {
			//our finalizer is present, so handle external dependency
			if workflow.Status.WorkflowID != "" {
				if err := r.deleteWorkflow(&workflow); err != nil {
					//if fail to delete the external dependency here, return with error so it can be retried
					return ctrl.Result{}, err
				}
			}
			//remove finalizer from the list and update it
			controllerutil.RemoveFinalizer(&workflow, deleteFinalizer)
			if err := r.Update(ctx, &workflow); err != nil {
				return ctrl.Result{}, err
			}
		}

		//stop reconciliation as item is being deleted
		return ctrl.Result{}, nil
	}

	if workflow.Status.WorkflowID != "" {
		existingWorkflowId, err := r.getExistingWorkflowById(&workflow) //nolint:golint
		if err != nil {
			r.Log.Error(err, "failed to fetch existing workflowId")
			return ctrl.Result{}, err
		}

		if existingWorkflowId != "" { //workflow exists in NR, check if updates are needed
			if len(workflow.Spec.Channels) > 0 {
				if r.haveChannelsChanged(&workflow) {
					r.Log.Info("channel spec changed, updating channels")
					var configuredDestinations alertsv1.AlertDestinationList
					if err := r.List(ctx, &configuredDestinations, client.InNamespace(req.Namespace)); err != nil {
						return ctrl.Result{}, err
					}
					r.handleChannels(&workflow, &configuredDestinations)
					r.updateWorkflow(&workflow)

					//Update Workflow Status
					err = r.Status().Update(ctx, &workflow)
					if err != nil {
						r.Log.Error(err, "failed to update AlertWorkflow status")
						return ctrl.Result{}, err
					}
				} else {
					r.Log.Info("channels unchanged - checking if workflow config changed")
					if r.hasWorkflowChanged(&workflow) {
						r.Log.Info("config mismatch, updating workflow")
						r.updateWorkflow(&workflow)

						//Update Workflow Status
						err = r.Status().Update(ctx, &workflow)
						if err != nil {
							r.Log.Error(err, "failed to update AlertWorkflow status")
							return ctrl.Result{}, err
						}
					}
				}
			}
		} else {
			r.Log.Info("Workflow ID does not exist in New Relic. May have been deleted outside of operator, creating...")
			var configuredDestinations alertsv1.AlertDestinationList
			if err := r.List(ctx, &configuredDestinations, client.InNamespace(req.Namespace)); err != nil {
				return ctrl.Result{}, err
			}
			channelErr := r.createChannels(&workflow, &configuredDestinations)
			if channelErr != nil {
				return ctrl.Result{}, err
			}

			workflowErr := r.createWorkflow(&workflow)
			if workflowErr != nil {
				return ctrl.Result{}, err
			}

			//Update Workflow Status
			err = r.Status().Update(ctx, &workflow)
			if err != nil {
				r.Log.Error(err, "failed to update AlertWorkflow status")
				return ctrl.Result{}, err
			}
		}

	} else { //no workflowId in state, so create -- TODO: need to be able to filter by name when fetching, client currently only allows ID
		r.Log.Info("No workflow id in state - Creating new workflow")
		var configuredDestinations alertsv1.AlertDestinationList
		if err := r.List(ctx, &configuredDestinations, client.InNamespace(req.Namespace)); err != nil {
			return ctrl.Result{}, err
		}
		channelErr := r.createChannels(&workflow, &configuredDestinations)
		if channelErr != nil {
			return ctrl.Result{}, err
		}

		workflowErr := r.createWorkflow(&workflow)
		if workflowErr != nil {
			return ctrl.Result{}, err
		}

		//Update Workflow Status
		err = r.Status().Update(ctx, &workflow)
		if err != nil {
			r.Log.Error(err, "failed to update AlertWorkflow status")
			return ctrl.Result{}, err
		}

	}

	//Update Workflow Status
	r.Log.Info("Updating status...")
	err = r.Status().Update(ctx, &workflow)
	if err != nil {
		r.Log.Error(err, "failed to update AlertWorkflow status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AlertWorkflowReconciler) hasWorkflowChanged(workflow *alertsv1.AlertWorkflow) bool {
	configuredWorkflow := workflow.Spec
	observedWorkflow := workflow.Status

	if configuredWorkflow.APIKey != observedWorkflow.AppliedSpec.APIKey ||
		configuredWorkflow.APIKeySecret != observedWorkflow.AppliedSpec.APIKeySecret ||
		configuredWorkflow.AccountID != observedWorkflow.AppliedSpec.AccountID ||
		configuredWorkflow.Region != observedWorkflow.AppliedSpec.Region ||
		configuredWorkflow.Name != observedWorkflow.AppliedSpec.Name ||
		configuredWorkflow.Enabled != observedWorkflow.AppliedSpec.Enabled ||
		!reflect.DeepEqual(configuredWorkflow.IssuesFilter, observedWorkflow.AppliedSpec.IssuesFilter) ||
		!reflect.DeepEqual(configuredWorkflow.Enrichments, observedWorkflow.AppliedSpec.Enrichments) ||
		configuredWorkflow.EnrichmentsEnabled != observedWorkflow.AppliedSpec.EnrichmentsEnabled ||
		configuredWorkflow.MutingRulesHandling != observedWorkflow.AppliedSpec.MutingRulesHandling ||
		configuredWorkflow.ChannelsEnabled != observedWorkflow.AppliedSpec.ChannelsEnabled {
		return true
	}

	for i := range configuredWorkflow.Channels {
		if i < len(observedWorkflow.AppliedSpec.Channels) { //if a new configured channel is not in current status
			if len(configuredWorkflow.Channels[i].NotificationTriggers) != len(observedWorkflow.AppliedSpec.Channels[i].NotificationTriggers) ||
				configuredWorkflow.Channels[i].UpdateOriginalMessage != observedWorkflow.AppliedSpec.Channels[i].UpdateOriginalMessage {
				return true
			}
		}
	}

	return false
}

func (r *AlertWorkflowReconciler) haveChannelsChanged(workflow *alertsv1.AlertWorkflow) bool {
	configuredChannels := workflow.Spec.Channels
	observedChannels := workflow.Status.AppliedSpec.Channels

	if len(configuredChannels) != len(observedChannels) {
		return true
	}

	for i := range configuredChannels {
		if !reflect.DeepEqual(configuredChannels[i].Spec, observedChannels[i].Spec) {
			return true
		}
	}

	return false

}

// This holds details for channels eligible to delete (no longer exist)
type processedAlertChannels struct {
	accountId int //nolint:golint
	id        string
	name      string
	processed bool
}

func (r *AlertWorkflowReconciler) handleChannels(workflow *alertsv1.AlertWorkflow, configuredDestinations *alertsv1.AlertDestinationList) error {
	processedChannels := []processedAlertChannels{}
	for i, existing := range workflow.Status.AppliedSpec.Channels {
		processedChannels = append(processedChannels, processedAlertChannels{
			accountId: workflow.Spec.AccountID,
			id:        existing.ChannelID,
			name:      existing.Spec.Name,
			processed: false,
		})
		if i < len(workflow.Spec.Channels) {
			if existing.Spec.Name == workflow.Spec.Channels[i].Spec.Name {
				workflow.Spec.Channels[i].ChannelID = existing.ChannelID
			}
		}
	}

	for i, configuredChannel := range workflow.Spec.Channels {
		r.Log.Info("Processing channel", "channelId", configuredChannel.ChannelID)
		existingChannelId, err := r.getExistingChannel(&configuredChannel, workflow.Spec.AccountID) //nolint:golint
		if err != nil {
			r.Log.Error(err, "failed to fetch existing channel")
			return err
		}

		if existingChannelId != "" { //channel exists in NR
			for _, observedChannel := range workflow.Status.AppliedSpec.Channels {
				if observedChannel.ChannelID == configuredChannel.ChannelID {
					processedChannels[i].processed = true

					if !reflect.DeepEqual(configuredChannel.Spec, observedChannel.Spec) {
						r.Log.Info("Channel configs differ, updating channel")
						updatedChannel, err := r.updateChannel(&configuredChannel, workflow.Spec.AccountID)
						if err != nil {
							return err
						}
						workflow.Spec.Channels[i] = *updatedChannel
					}
				}
			}
		} else { //channel doesn't exist in NR (newly added to spec), create it
			r.Log.Info("Channel does not exist, creating channel", "channelName", configuredChannel.Spec.Name)
			createdChannel, err := r.createChannel(&configuredChannel, workflow.Spec.AccountID, configuredDestinations)
			if err != nil {
				r.Log.Error(err, "Failed to create channel",
					"channelName", configuredChannel.Spec.Name,
				)
			}
			configuredChannel.ChannelID = createdChannel.ChannelID
			workflow.Spec.Channels[i] = *createdChannel
		}
	}

	r.Log.Info("Checking if any channels need to be deleted")
	if len(processedChannels) > 0 {
		for _, pc := range processedChannels {
			if !pc.processed {
				if pc.id != "" {
					err := r.deleteChannel(pc)
					if err != nil {
						return err
					}
				} else {
					r.Log.Info("channelId empty - cannot delete channel", "channelName", pc.name)
				}
			}
		}
	}

	workflow.Status.AppliedSpec = &workflow.Spec

	return nil
}

func (r *AlertWorkflowReconciler) createChannels(workflow *alertsv1.AlertWorkflow, configuredDestinations *alertsv1.AlertDestinationList) error {
	r.Log.Info("Creating destination channels")

	workflow.Status.AppliedSpec = &alertsv1.AlertWorkflowSpec{}
	workflow.Status.AppliedSpec.Channels = workflow.Spec.Channels

	for i, channel := range workflow.Spec.Channels {
		createdChannel, err := r.createChannel(&channel, workflow.Spec.AccountID, configuredDestinations)
		if err != nil {
			r.Log.Error(err, "Failed to create channel",
				"channelName", channel.Spec.Name,
			)
			return err
		}
		if createdChannel != nil {
			channel.ChannelID = createdChannel.ChannelID
			workflow.Spec.Channels[i] = *createdChannel
			workflow.Status.AppliedSpec.Channels[i] = *createdChannel
		}
	}

	return nil
}

func (r *AlertWorkflowReconciler) createChannel(channel *alertsv1.DestinationChannel, accountID int, configuredDestinations *alertsv1.AlertDestinationList) (*alertsv1.DestinationChannel, error) {
	r.Log.Info("Checking for existing destination", "destinationName", channel.Spec.DestinationName)
	if len(configuredDestinations.Items) > 0 { //Check for existing operator created destinations
		for _, destination := range configuredDestinations.Items {
			if destination.Status.AppliedSpec.Name == channel.Spec.DestinationName {
				r.Log.Info("Existing destination found", "destinationId", destination.Status.DestinationID)
				channel.Spec.DestinationID = destination.Status.DestinationID
				break
			}
		}
	} else { //check for existing destinations in NR (created outside of operator)
		matchedDestinationID, err := r.getExistingDestination(channel, accountID)
		if err != nil {
			r.Log.Error(err, "failed to fetch existing destination, cannot create channel without an existing destination")
			return nil, err
		}

		if matchedDestinationID != "" {
			r.Log.Info("Existing destination found in New Relic", "destinationId", matchedDestinationID)
			channel.Spec.DestinationID = matchedDestinationID
		} else {
			return nil, errors.New("no destination exists in New Relic to create channel, workflow will not be created")
		}
	}

	r.Log.Info("Creating channel", "channelName", channel.Spec.Name)

	chanInput := r.translateChannelCreateInput(channel)

	createdChannel, err := r.Alerts.Notifications().AiNotificationsCreateChannel(accountID, chanInput)
	if err != nil {
		r.Log.Error(err, "failed to create channel",
			"destinationId", channel.Spec.DestinationID,
			"accountId", channel.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return nil, err
	}

	r.Log.Info("createdChannel", "channel", createdChannel)

	channel.ChannelID = createdChannel.Channel.ID

	return channel, nil
}

// getExistingDestination fetches existing destinations
func (r *AlertWorkflowReconciler) getExistingDestination(channel *alertsv1.DestinationChannel, accountId int) (string, error) { //nolint:golint
	searchParams := ai.AiNotificationsDestinationFilter{
		Name: channel.Spec.DestinationName,
	}

	sorter := notifications.AiNotificationsDestinationSorter{}

	existingDests, err := r.Alerts.Notifications().GetDestinations(accountId, "", searchParams, sorter)
	if err != nil {
		r.Log.Error(err, "failed to get list of destinations to create channel",
			"destinationName", channel.Spec.DestinationName,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return "", err
	}

	if len(existingDests.Entities) > 0 {
		for _, existingDest := range existingDests.Entities {
			if existingDest.Name == channel.Spec.DestinationName {
				r.Log.Info("Found destination based on name, returning")
				channel.Spec.DestinationID = existingDest.ID
				break
			}
		}

		return channel.Spec.DestinationID, nil
	}

	return "", nil
}

func (r *AlertWorkflowReconciler) updateChannel(channel *alertsv1.DestinationChannel, accountID int) (*alertsv1.DestinationChannel, error) {
	r.Log.Info("Updating channel", "channelName", channel.Spec.Name)

	chanInput := r.translateChannelUpdateInput(channel)

	updatedChannel, err := r.Alerts.Notifications().AiNotificationsUpdateChannel(accountID, chanInput, channel.ChannelID)
	if err != nil {
		r.Log.Error(err, "failed to updated channel",
			"destinationId", channel.Spec.DestinationID,
			"accountId", channel.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return nil, err
	}

	r.Log.Info("updatedChannel", "channel", updatedChannel)

	channel.ChannelID = updatedChannel.Channel.ID

	return channel, nil
}

func (r *AlertWorkflowReconciler) deleteChannel(channel processedAlertChannels) error {
	r.Log.Info("Deleting channel", "channelId", channel.id)

	_, err := r.Alerts.Notifications().AiNotificationsDeleteChannel(channel.accountId, channel.id)
	if err != nil {
		r.Log.Error(err, "failed to delete channel",
			"channelId", channel.id,
			"accountId", channel.accountId,
		)
		return err
	}

	return nil
}

func (r *AlertWorkflowReconciler) translateChannelCreateInput(channel *alertsv1.DestinationChannel) notifications.AiNotificationsChannelInput {
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

func (r *AlertWorkflowReconciler) translateChannelUpdateInput(channel *alertsv1.DestinationChannel) notifications.AiNotificationsChannelUpdate {
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

func (r *AlertWorkflowReconciler) getExistingChannel(channel *alertsv1.DestinationChannel, accountID int) (string, error) {
	r.Log.Info("Checking for existing channel", "channelName", channel.Spec.Name)

	searchParams := ai.AiNotificationsChannelFilter{
		ID: channel.ChannelID,
	}

	sorter := notifications.AiNotificationsChannelSorter{}

	existingChannels, err := r.Alerts.Notifications().GetChannels(accountID, "", searchParams, sorter)
	if err != nil {
		r.Log.Error(err, "failed to get channel",
			"channelName", channel.Spec.Name,
			"channelId", channel.ChannelID,
			"accountId", accountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
	}

	if len(existingChannels.Entities) > 0 {
		for _, existingChannel := range existingChannels.Entities {
			if existingChannel.ID == channel.ChannelID {
				r.Log.Info("Found channel, returning")
				channel.ChannelID = existingChannel.ID
				break
			}
		}

		return channel.ChannelID, nil
	}

	return "", nil
}

// createWorkflow creates a new workflow
func (r *AlertWorkflowReconciler) createWorkflow(workflow *alertsv1.AlertWorkflow) error {
	r.Log.Info("Creating workflow", "workflowName", workflow.Spec.Name)

	workflowInput := r.translateWorkflowCreateInput(workflow)

	createdWorkflow, err := r.Alerts.Workflows().AiWorkflowsCreateWorkflow(workflow.Spec.AccountID, workflowInput)
	if err != nil {
		r.Log.Error(err, "failed to create workflow",
			"workflowName", workflow.Spec.Name,
			"accountId", workflow.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	if len(createdWorkflow.Errors) > 0 {
		err = errors.New(createdWorkflow.Errors[0].Description)
		r.Log.Error(err, "failed to create workflow",
			"workflowName", workflow.Spec.Name,
			"accountId", workflow.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("createdWorkflow", "workflow", createdWorkflow)

	workflow.Status.AppliedSpec = &workflow.Spec
	workflow.Status.WorkflowID = createdWorkflow.Workflow.ID

	r.Log.Info("Successfully created New Relic workflow", "workflowID", createdWorkflow.Workflow.ID)

	return nil
}

// updateWorkflow updates a workflow and any associated channels
func (r *AlertWorkflowReconciler) updateWorkflow(workflow *alertsv1.AlertWorkflow) error {
	r.Log.Info("Updating workflow", "workflowName", workflow.Spec.Name)

	//need to add channelids to spec before updating/re-applying status
	if len(workflow.Status.AppliedSpec.Channels) > 0 {
		for c, channel := range workflow.Status.AppliedSpec.Channels {
			workflow.Spec.Channels[c].ChannelID = channel.ChannelID
		}
	}

	workflowInput := r.translateWorkflowUpdateInput(workflow)

	updatedWorkflow, err := r.Alerts.Workflows().AiWorkflowsUpdateWorkflow(workflow.Spec.AccountID, true, workflowInput)
	if err != nil {
		r.Log.Error(err, "failed to update workflow",
			"workflowName", workflow.Spec.Name,
			"accountId", workflow.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	if len(updatedWorkflow.Errors) > 0 {
		err = errors.New(updatedWorkflow.Errors[0].Description)
		r.Log.Error(err, "failed to create workflow",
			"workflowName", workflow.Spec.Name,
			"accountId", workflow.Spec.AccountID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	r.Log.Info("updatedWorkflow", "workflow", updatedWorkflow)

	workflow.Status.AppliedSpec = &workflow.Spec
	workflow.Status.WorkflowID = updatedWorkflow.Workflow.ID

	r.Log.Info("Successfully updated New Relic workflow", "workflowID", updatedWorkflow.Workflow.ID)

	// if len(workflow.Spec.Channels) > 0 {
	// 	r.Log.Info("Checking if channels need to be updated...")
	// 	if r.haveChannelsChanged(workflow) {
	// 		r.Log.Info("channel spec changed, updating channels")
	// 		r.handleChannels(workflow)
	// 	} else {
	// 		r.Log.Info("channels unchanged - skipping updates")
	// 		return nil
	// 	}
	// }

	return nil
}

// deleteWorkflow deletes a workflow and associated channels
func (r *AlertWorkflowReconciler) deleteWorkflow(workflow *alertsv1.AlertWorkflow) error {
	r.Log.Info("Deleting Workflow", "workflowName", workflow.Spec.Name)
	_, err := r.Alerts.Workflows().AiWorkflowsDeleteWorkflow(workflow.Spec.AccountID, true, workflow.Status.WorkflowID)
	if err != nil {
		return err
	}

	return nil
}

func (r *AlertWorkflowReconciler) translateWorkflowCreateInput(workflow *alertsv1.AlertWorkflow) workflows.AiWorkflowsCreateWorkflowInput {
	workflowInput := workflows.AiWorkflowsCreateWorkflowInput{}
	workflowInput.Name = workflow.Spec.Name
	workflowInput.WorkflowEnabled = workflow.Spec.Enabled

	if workflow.Spec.IssuesFilter != nil {
		workflowInput.IssuesFilter = workflows.AiWorkflowsFilterInput{}
		workflowInput.IssuesFilter.Name = workflow.Spec.IssuesFilter.Name
		workflowInput.IssuesFilter.Type = workflow.Spec.IssuesFilter.Type
		for _, filter := range workflow.Spec.IssuesFilter.Predicates {
			filters := workflows.AiWorkflowsPredicateInput{}
			filters.Attribute = filter.Attribute
			filters.Operator = filter.Operator
			filters.Values = filter.Values

			workflowInput.IssuesFilter.Predicates = append(workflowInput.IssuesFilter.Predicates, filters)
		}
	}

	if len(workflow.Spec.Enrichments.NRQL) > 0 {
		workflowInput.Enrichments = &workflows.AiWorkflowsEnrichmentsInput{}
		for _, nrql := range workflow.Spec.Enrichments.NRQL {
			nrqls := workflows.AiWorkflowsNRQLEnrichmentInput{}
			nrqls.Name = nrql.Name

			for _, config := range nrql.Configuration {
				configs := workflows.AiWorkflowsNRQLConfigurationInput{}
				configs.Query = config.Query
				nrqls.Configuration = append(nrqls.Configuration, configs)
			}

			workflowInput.Enrichments.NRQL = append(workflowInput.Enrichments.NRQL, nrqls)
		}
	}

	workflowInput.EnrichmentsEnabled = workflow.Spec.EnrichmentsEnabled
	workflowInput.MutingRulesHandling = workflow.Spec.MutingRulesHandling
	workflowInput.WorkflowEnabled = workflow.Spec.Enabled

	if workflow.Spec.Channels != nil {
		for _, channel := range workflow.Spec.Channels {
			channels := workflows.AiWorkflowsDestinationConfigurationInput{}
			channels.ChannelId = channel.ChannelID
			channels.UpdateOriginalMessage = channel.UpdateOriginalMessage
			channels.NotificationTriggers = channel.NotificationTriggers

			workflowInput.DestinationConfigurations = append(workflowInput.DestinationConfigurations, channels)
		}
	}

	return workflowInput
}

func (r *AlertWorkflowReconciler) translateWorkflowUpdateInput(workflow *alertsv1.AlertWorkflow) workflows.AiWorkflowsUpdateWorkflowInput {
	workflowInput := workflows.AiWorkflowsUpdateWorkflowInput{}
	workflowInput.ID = workflow.Status.WorkflowID
	workflowInput.Name = &workflow.Spec.Name
	workflowInput.WorkflowEnabled = &workflow.Spec.Enabled

	if workflow.Spec.IssuesFilter != nil {
		workflowInput.IssuesFilter = &workflows.AiWorkflowsUpdatedFilterInput{}
		workflowInput.IssuesFilter.FilterInput = workflows.AiWorkflowsFilterInput{}
		workflowInput.IssuesFilter.FilterInput.Name = workflow.Spec.IssuesFilter.Name
		workflowInput.IssuesFilter.FilterInput.Type = workflow.Spec.IssuesFilter.Type
		for _, filter := range workflow.Spec.IssuesFilter.Predicates {
			filters := workflows.AiWorkflowsPredicateInput{}
			filters.Attribute = filter.Attribute
			filters.Operator = filter.Operator
			filters.Values = filter.Values

			workflowInput.IssuesFilter.FilterInput.Predicates = append(workflowInput.IssuesFilter.FilterInput.Predicates, filters)
		}
	}

	if len(workflow.Spec.Enrichments.NRQL) > 0 {
		workflowInput.Enrichments = &workflows.AiWorkflowsUpdateEnrichmentsInput{}
		for _, nrql := range workflow.Spec.Enrichments.NRQL {
			nrqls := workflows.AiWorkflowsNRQLUpdateEnrichmentInput{}
			nrqls.Name = nrql.Name

			for _, config := range nrql.Configuration {
				configs := workflows.AiWorkflowsNRQLConfigurationInput{}
				configs.Query = config.Query
				nrqls.Configuration = append(nrqls.Configuration, configs)
			}

			workflowInput.Enrichments.NRQL = append(workflowInput.Enrichments.NRQL, nrqls)
		}
	}

	workflowInput.EnrichmentsEnabled = &workflow.Spec.EnrichmentsEnabled
	workflowInput.MutingRulesHandling = workflow.Spec.MutingRulesHandling
	workflowInput.WorkflowEnabled = &workflow.Spec.Enabled

	if workflow.Spec.Channels != nil {
		if workflowInput.DestinationConfigurations == nil {
			workflowInput.DestinationConfigurations = &[]workflows.AiWorkflowsDestinationConfigurationInput{}
		}
		for i, channel := range workflow.Spec.Channels {
			channels := &workflows.AiWorkflowsDestinationConfigurationInput{}
			channels.ChannelId = workflow.Status.AppliedSpec.Channels[i].ChannelID
			channels.UpdateOriginalMessage = channel.UpdateOriginalMessage
			channels.NotificationTriggers = channel.NotificationTriggers

			*workflowInput.DestinationConfigurations = append(*workflowInput.DestinationConfigurations, *channels)
		}
	}

	return workflowInput
}

// getExistingWorkflowById fetches an existing workflow by id
func (r *AlertWorkflowReconciler) getExistingWorkflowById(workflow *alertsv1.AlertWorkflow) (string, error) { //nolint:golint
	r.Log.Info("Checking for existing workflow", "workflowName", workflow.Spec.Name)

	searchParams := ai.AiWorkflowsFilters{ //TODO: switch to name?
		ID: workflow.Status.WorkflowID,
	}

	existingWorkflows, err := r.Alerts.Workflows().GetWorkflows(workflow.Spec.AccountID, "", searchParams)
	if err != nil {
		r.Log.Error(err, "failed to get workflow",
			"workflowName", workflow.Spec.Name,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return "", err
	}

	if len(existingWorkflows.Entities) > 0 {
		for _, existingWorkflow := range existingWorkflows.Entities {
			if existingWorkflow.ID == workflow.Status.WorkflowID {
				r.Log.Info("Found workflow, returning")
				workflow.Status.WorkflowID = existingWorkflow.ID
				break
			}
		}

		return workflow.Status.WorkflowID, nil
	}

	return "", nil
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *AlertWorkflowReconciler) getAPIKeyOrSecret(workflow alertsv1.AlertWorkflow) (string, error) {
	if workflow.Spec.APIKey != "" {
		return workflow.Spec.APIKey, nil
	}

	if workflow.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: workflow.Spec.APIKeySecret.Namespace, Name: workflow.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[workflow.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}

	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AlertWorkflowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.AlertWorkflow{}).
		Complete(r)
}
