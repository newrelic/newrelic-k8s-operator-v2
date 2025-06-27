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
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/common"
	"github.com/newrelic/newrelic-client-go/v2/pkg/entities"
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
	"github.com/newrelic/newrelic-k8s-operator-v2/interfaces"
)

// EntityTaggingReconciler reconciles a EntityTagging object
type EntityTaggingReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	Entities     interfaces.NewRelicClientInterface
	EntityClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey       string
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=entitytaggings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=entitytaggings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=entitytaggings/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the EntityTagging object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *EntityTaggingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	//Fetch EntityTagging instance
	var entity alertsv1.EntityTagging
	err := r.Get(ctx, req.NamespacedName, &entity)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("Entity Tags 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET entity tags", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(entity)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	entityClient, errEntityClient := r.EntityClient(r.apiKey, entity.Spec.Region)
	if errEntityClient != nil {
		r.Log.Error(errEntityClient, "Failed to create Entity Client")
		return ctrl.Result{}, errEntityClient
	}
	r.Entities = entityClient

	//Handle entity tags deletion
	deleteFinalizer := "newrelic.entitytags.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if entity.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&entity, deleteFinalizer) {
			r.Log.Info("Adding finalizer for EntityTagging resource")
			controllerutil.AddFinalizer(&entity, deleteFinalizer)
			if err := r.Update(ctx, &entity); err != nil {
				r.Log.Error(err, "Failed to add finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	} else {
		//Entity Tags are being deleted
		if controllerutil.ContainsFinalizer(&entity, deleteFinalizer) {
			if len(entity.Status.ManagedEntities) > 0 {
				var tagKeys []string
				if entity.Status.AppliedSpec != nil {
					tagKeys = r.getTagKeys(entity.Status.AppliedSpec.Tags)
				}

				if len(tagKeys) > 0 {
					for _, managedEntity := range entity.Status.ManagedEntities {
						if err := r.deleteEntityTags(managedEntity.GUID, tagKeys); err != nil {
							// Log the error but don't block finalizer removal
							r.Log.Error(err, "Finalizer failed to delete tags, continuing", "guid", managedEntity.GUID)
						}
					}
				}
			}
			controllerutil.RemoveFinalizer(&entity, deleteFinalizer)
			if err := r.Update(ctx, &entity); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// MAIN RECONCILIATION LOGIC

	originalStatus := *entity.Status.DeepCopy()

	if entity.Status.ManagedEntities == nil { // first run - add all tags in spec
		r.Log.Info("First-time reconciliation: fetching entities and applying all specified tags.")

		// Fetch all entities in spec by name
		fetchedEntities, err := r.fetchEntitiesByName(entity.Spec.EntityNames)
		if err != nil {
			r.Log.Error(err, "Failed to fetch entities during initial creation.")
			return ctrl.Result{}, err
		}

		if len(fetchedEntities) == 0 {
			r.Log.Info("No matching entities found in New Relic for entityNames provided. Skipping reconciliation and will retry in 5 minutes")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}

		// Log which entities were not found, if any (don't exist in NR)
		foundEntitiesMap := make(map[string]bool)
		for _, fe := range fetchedEntities {
			foundEntitiesMap[fe.Name] = true
		}
		for _, name := range entity.Spec.EntityNames {
			if !foundEntitiesMap[name] {
				r.Log.Info("Specified entity name was not found in New Relic", "name", name)
			}
		}

		// For each found entity, apply all tags from the spec.
		var allErrors []error
		if len(fetchedEntities) > 0 {
			r.Log.Info("Applying tags to all found entities.", "count", len(fetchedEntities))
			for _, fe := range fetchedEntities {
				if err := r.addEntityTags(fe.GUID, entity.Spec.Tags); err != nil {
					allErrors = append(allErrors, fmt.Errorf("failed to apply initial tags to guid %s: %w", fe.GUID, err))
				}
			}
		}

		// If any tagging operation failed, return an error and retry.
		if len(allErrors) > 0 {
			r.Log.Error(allErrors[0], "One or more errors occurred during initial tagging")
			return ctrl.Result{}, allErrors[0]
		}

		// If all successful, update the status to reflect the completed work.
		r.Log.Info("Initial tagging successful. Updating status...")
		entity.Status.ManagedEntities = fetchedEntities
		entity.Status.AppliedSpec = &entity.Spec

		if err := r.Status().Update(ctx, &entity); err != nil {
			r.Log.Error(err, "Failed to update status after initial creation.")
			return ctrl.Result{}, err
		}

		// trigger a new reconcile to more deeply reconcile tags
		return ctrl.Result{}, nil

	} else { // entities exist in status
		r.Log.Info("Reconciling existing entities")
		if err := r.reconcileEntities(&entity); err != nil {
			return ctrl.Result{}, err
		}

		if len(entity.Status.ManagedEntities) > 0 {
			if err := r.handleTags(&entity); err != nil {
				return ctrl.Result{}, err
			}
		}

		if !reflect.DeepEqual(originalStatus.AppliedSpec, &entity.Spec) {
			entity.Status.AppliedSpec = &entity.Spec
		}

		if !reflect.DeepEqual(originalStatus, entity.Status) {
			if err := r.Status().Update(ctx, &entity); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			r.Log.Info("No status changes detected, reconciliation complete.")
		}
	}

	return ctrl.Result{RequeueAfter: time.Hour}, nil
}

// buildEntityNameFilter constructs the entity name filter used in entitySearch to obtain entity guids
func buildEntityNameFilter(names []string) string {
	if len(names) == 0 {
		return ""
	}

	quotedNames := make([]string, len(names))
	for i, name := range names {
		quotedNames[i] = "'" + name + "'"
	}

	return strings.Join(quotedNames, ",")
}

// comapreTagValues compares 2 sets of tag values and returns true if they contain the same strings, regardless of order [NR API returns tag values out of order of local state]
func compareTagValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	aCopy := make([]string, len(a))
	copy(aCopy, a)
	bCopy := make([]string, len(b))
	copy(bCopy, b)

	sort.Strings(aCopy)
	sort.Strings(bCopy)

	return reflect.DeepEqual(aCopy, bCopy)
}

// fetchEntitiesByName queries for a list of entityNames and returns a slice of ManagedEntity structs
// NOTE: only one unique guid is returned per entity name, so it is expected that you don't have duplicate entities with the same name
func (r *EntityTaggingReconciler) fetchEntitiesByName(entityNames []string) ([]alertsv1.ManagedEntity, error) {
	r.Log.Info("Fetching remote entities to reconcile")

	nameFilter := buildEntityNameFilter(entityNames)
	query := fmt.Sprintf("reporting is true and name in (%s)", nameFilter)

	result, err := r.Entities.Entities().GetEntitySearchByQuery(entities.EntitySearchOptions{}, query, nil)
	if err != nil {
		r.Log.Error(err, "failed to fetch entities")
		return nil, err
	}

	var managedEntities []alertsv1.ManagedEntity
	processedNames := make(map[string]bool)

	if len(result.Results.Entities) > 0 {
		for _, entity := range result.Results.Entities {
			entityName := entity.GetName()

			//check if there is already a guid stored for a given entity name, skip it
			if _, found := processedNames[entityName]; found {
				r.Log.Info("Duplicate entity found, ignore subsequent GUID(s). Only the first result returned is tagged and stored.",
					"name", entityName,
					"ignoredGuid", entity.GetGUID())
				continue
			}

			managedEntities = append(managedEntities, alertsv1.ManagedEntity{
				Name: entity.GetName(),
				GUID: entity.GetGUID(),
			})

			processedNames[entityName] = true // mark name as processed to ignore any future occurrences
		}
	}

	return managedEntities, nil
}

// reconcileEntities compares spec.EntityNames with status.ManagedEntities
// guids are fetched for new entities, and tags are cleaned up for removed entities
// returns true if the entity status was modified
func (r *EntityTaggingReconciler) reconcileEntities(entity *alertsv1.EntityTagging) error {
	r.Log.Info("Starting entity tagging reconcile")
	desiredNames := make(map[string]bool)
	for _, name := range entity.Spec.EntityNames {
		desiredNames[name] = true
	}

	managedEntitiesMap := make(map[string]common.EntityGUID)
	if entity.Status.ManagedEntities != nil {
		for _, me := range entity.Status.ManagedEntities {
			managedEntitiesMap[me.Name] = me.GUID
		}
	}

	final := []alertsv1.ManagedEntity{}
	var entityListChanged bool

	// Handle removals
	for name, guid := range managedEntitiesMap {
		if _, isDesired := desiredNames[name]; !isDesired {
			r.Log.Info("Entity removed from spec, cleaning up tags", "name", name, "guid", guid)
			entityListChanged = true

			var tagsToDelete []string
			if entity.Status.AppliedSpec != nil {
				tagsToDelete = r.getTagKeys(entity.Status.AppliedSpec.Tags)
			}

			if len(tagsToDelete) > 0 {
				if err := r.deleteEntityTags(guid, tagsToDelete); err != nil {
					return err
				}
			}
		} else {
			// Entity in both spec/status, so keep it in final status
			final = append(final, alertsv1.ManagedEntity{Name: name, GUID: guid})
		}
	}

	// Handle additions/updates
	var newEntityNames []string
	for name := range desiredNames {
		if _, isManaged := managedEntitiesMap[name]; !isManaged {
			r.Log.Info("New entity found in spec", "name", name)
			entityListChanged = true
			newEntityNames = append(newEntityNames, name)
		}
	}

	// fetch guids
	if len(newEntityNames) > 0 {
		fetchedEntities, err := r.fetchEntitiesByName(newEntityNames)
		if err != nil {
			return err
		}

		foundEntities := make(map[string]bool)
		for _, fetched := range fetchedEntities {
			foundEntities[fetched.Name] = true
		}

		for _, requestedName := range newEntityNames {
			if _, found := foundEntities[requestedName]; !found {
				r.Log.Info("Specified entity was not found in New Relic - verify name is correct", "name", requestedName)
			}
		}

		final = append(final, fetchedEntities...)
	}

	if entityListChanged {
		entity.Status.ManagedEntities = final
	}

	return nil
}

// getTagKeys returns a list of tag keys
func (r *EntityTaggingReconciler) getTagKeys(entityTags []*alertsv1.EntityTag) []string {
	var tagKeys []string

	if len(entityTags) > 0 {
		for _, tag := range entityTags {
			tagKeys = append(tagKeys, tag.Key)
		}
	}

	return tagKeys
}

// handleTags determines what tags are to be created, updated, or deleted by comparing the desired spec
// against the actual remote state on each entity, and then performs the corresponding actions.
func (r *EntityTaggingReconciler) handleTags(entity *alertsv1.EntityTagging) error {
	r.Log.Info("Reconciling tags for all entities against remote state")
	var allErrors []error

	// Create a map of desired tags from the spec for efficient lookup.
	desiredTagsMap := make(map[string]*alertsv1.EntityTag)
	for _, dt := range entity.Spec.Tags {
		desiredTagsMap[dt.Key] = dt
	}

	// Process each managed entity individually.
	for _, managedEntity := range entity.Status.ManagedEntities {
		guid := managedEntity.GUID
		r.Log.Info("Processing entity", "name", managedEntity.Name, "guid", guid)

		// Fetch the current mutable tags directly from the New Relic entity.
		remoteTags, err := r.Entities.Entities().GetTagsForEntityMutable(guid)
		if err != nil {
			r.Log.Error(err, "Failed to fetch remote tags for entity", "guid", guid)
			allErrors = append(allErrors, fmt.Errorf("failed to fetch remote tags for guid %s: %w", guid, err))
			continue
			//return err
		}

		// Convert remote tags into a map
		remoteTagsMap := make(map[string]*entities.EntityTag)
		for _, rt := range remoteTags {
			remoteTagsMap[rt.Key] = rt
		}

		var tagsToDelete []string
		var tagsToAdd []*alertsv1.EntityTag
		var tagsToUpdate []entities.TaggingTagInput

		// Find tags to delete -- tags should be deleted when they exist remotely but are not in the defined spec. This corrects manual additions.
		for remoteKey := range remoteTagsMap {
			if _, isDesired := desiredTagsMap[remoteKey]; !isDesired {
				r.Log.Info("Tag marked for deletion (drift detected)", "guid", guid, "key", remoteKey)
				tagsToDelete = append(tagsToDelete, remoteKey)
			}
		}

		// Find tags to Add or Update -- compare desired spec against the remote state.
		for desiredKey, desiredTag := range desiredTagsMap {
			remoteTag, foundRemotely := remoteTagsMap[desiredKey]

			if !foundRemotely {
				// The tag from our spec doesn't exist remotely. It needs to be added.
				r.Log.Info("Tag marked for addition (drift detected)", "guid", guid, "key", desiredKey)
				tagsToAdd = append(tagsToAdd, desiredTag)
			} else {
				// The tag exists in both places. Compare values to see if an update is needed.
				if !compareTagValues(desiredTag.Values, remoteTag.Values) {
					r.Log.Info("Tag marked for update (drift detected)", "guid", guid, "key", desiredKey, "desiredValues", desiredTag.Values, "remoteValues", remoteTag.Values)
					tagsToUpdate = append(tagsToUpdate, entities.TaggingTagInput{
						Key:    desiredTag.Key,
						Values: desiredTag.Values,
					})
				}
			}
		}

		// Execute the calculated changes for the current entity.
		if len(tagsToDelete) > 0 {
			r.Log.Info("Executing tag deletion(s) to correct drift", "guid", guid, "count", len(tagsToDelete))
			if err := r.deleteEntityTags(guid, tagsToDelete); err != nil {
				allErrors = append(allErrors, err)
				//return err
			}
		}

		if len(tagsToAdd) > 0 {
			r.Log.Info("Executing tag addition(s)", "guid", guid, "count", len(tagsToAdd))
			if err := r.addEntityTags(guid, tagsToAdd); err != nil {
				allErrors = append(allErrors, err)
				//return err
			}
		}

		if len(tagsToUpdate) > 0 {
			r.Log.Info("Executing tag update(s) to correct drift", "guid", guid, "count", len(tagsToUpdate))
			if err := r.updateEntityTags(guid, tagsToUpdate); err != nil {
				allErrors = append(allErrors, err)
				//return err
			}
		}
	}

	if len(allErrors) > 0 {
		r.Log.Error(allErrors[0], "One or more errors have occurred when reconciling tags")
		return allErrors[0]
	}

	return nil
}

// addEntityTags adds mutable tags to an entity
func (r *EntityTaggingReconciler) addEntityTags(guid common.EntityGUID, tags []*alertsv1.EntityTag) error {
	r.Log.Info("Adding tags", "tags", tags, "guid", guid)
	tagInput := r.translateTagInput(tags)

	_, err := r.Entities.Entities().TaggingAddTagsToEntity(guid, tagInput)
	if err != nil {
		r.Log.Error(err, "failed to add tags on entity", "guid", guid)
		return err
	}

	return nil
}

// updateEntityTags updates mutable values for a single key attached to an entity
func (r *EntityTaggingReconciler) updateEntityTags(guid common.EntityGUID, tags []entities.TaggingTagInput) error {
	r.Log.Info("Updating tags", "tags", tags, "guid", guid)

	_, err := r.Entities.Entities().TaggingReplaceTagsOnEntity(guid, tags)
	if err != nil {
		r.Log.Error(err, "failed to update tags on entity", "guid", guid)
		return err
	}

	return nil
}

// deleteEntityTags deletes mutable tags attached to an entity given a list of keys
func (r *EntityTaggingReconciler) deleteEntityTags(guid common.EntityGUID, tagKeys []string) error {
	r.Log.Info("Deleting tags", "tagKeys", tagKeys, "guid", guid)

	_, err := r.Entities.Entities().TaggingDeleteTagFromEntity(guid, tagKeys)
	if err != nil {
		r.Log.Error(err, "failed to delete tagKeys on entity", "guid", guid)
		return err
	}

	return nil
}

// translateTagInput returns formatted spec for adding or updating tags on entities
func (r *EntityTaggingReconciler) translateTagInput(tags []*alertsv1.EntityTag) []entities.TaggingTagInput {
	var t []entities.TaggingTagInput

	for _, tag := range tags {
		t = append(t, entities.TaggingTagInput{
			Key:    tag.Key,
			Values: tag.Values,
		})
	}

	return t
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *EntityTaggingReconciler) getAPIKeyOrSecret(entity alertsv1.EntityTagging) (string, error) {
	if entity.Spec.APIKey != "" {
		return entity.Spec.APIKey, nil
	}

	if entity.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: entity.Spec.APIKeySecret.Namespace, Name: entity.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[entity.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}

	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EntityTaggingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.EntityTagging{}).
		Complete(r)
}
