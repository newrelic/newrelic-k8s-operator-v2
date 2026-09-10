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
	"fmt"
	"reflect"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	kErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	nrerrors "github.com/newrelic/newrelic-client-go/v2/pkg/errors"
	"github.com/newrelic/newrelic-client-go/v2/pkg/servicelevel"
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
	"github.com/newrelic/newrelic-k8s-operator-v2/interfaces"
)

// ServiceLevelReconciler reconciles a ServiceLevel object
type ServiceLevelReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	Log                logr.Logger
	ServiceLevels      interfaces.NewRelicClientInterface
	ServiceLevelClient func(string, string) (interfaces.NewRelicClientInterface, error)
	apiKey             string
}

// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=servicelevels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=servicelevels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=alerts.k8s.newrelic.com,resources=servicelevels/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ServiceLevel object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *ServiceLevelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var sl alertsv1.ServiceLevel
	err := r.Get(ctx, req.NamespacedName, &sl)
	if err != nil {
		if kErr.IsNotFound(err) {
			r.Log.Info("ServiceLevel 'not found' after being deleted. This is expected and no cause for alarm", "error", err)
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "Failed to GET ServiceLevel", "name", req.NamespacedName.String())
		return ctrl.Result{}, err
	}

	r.Log.Info("Starting ServiceLevel reconcile")

	//get API key
	r.apiKey, err = r.getAPIKeyOrSecret(sl)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.apiKey == "" {
		return ctrl.Result{}, err
	}

	//init client
	slClient, errClient := r.ServiceLevelClient(r.apiKey, sl.Spec.Region)
	if errClient != nil {
		r.Log.Error(errClient, "Failed to create ServiceLevel Client")
		return ctrl.Result{}, errClient
	}
	r.ServiceLevels = slClient

	//Handle ServiceLevel deletion
	deleteFinalizer := "newrelic.servicelevel.finalizer"

	//Examine DeletionTimestamp to determine if object is under deletion
	if sl.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&sl, deleteFinalizer) {
			controllerutil.AddFinalizer(&sl, deleteFinalizer)
			if err := r.Update(ctx, &sl); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//ServiceLevel being deleted
		if controllerutil.ContainsFinalizer(&sl, deleteFinalizer) {
			if sl.Status.ServiceLevelGUID != "" {
				if _, err := r.ServiceLevels.ServiceLevel().ServiceLevelDelete(sl.Status.ServiceLevelGUID); err != nil {
					// Log the error but don't block finalizer removal, so a hand-deleted
					// SLI can't wedge the CR.
					r.Log.Error(err, "Finalizer failed to delete SLI, continuing", "guid", sl.Status.ServiceLevelGUID)
				}
			}

			controllerutil.RemoveFinalizer(&sl, deleteFinalizer)
			if err := r.Update(ctx, &sl); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	//no config changes
	if reflect.DeepEqual(&sl.Spec, sl.Status.AppliedSpec) {
		r.Log.Info("No config changes. Skipping reconcile")
		return ctrl.Result{}, nil
	}

	remote, err := r.getExistingIndicator(&sl)
	if err != nil {
		r.Log.Error(err, "failed to fetch existing SLI", "entityGuid", sl.Spec.EntityGUID)
		return ctrl.Result{}, err
	}

	if remote != nil && remote.Events.Account.ID != sl.Spec.AccountID {
		// ServiceLevelEventsUpdateInput has no AccountID field - the source account
		// is structurally unchangeable once an SLI exists. This can only happen via
		// a hand-created SLI under the wrong account, or a stale accountId from
		// before the webhook made it immutable. Either way it can't self-resolve,
		// so surface it as a log line and stop rather than hot-looping on an update
		// call that will never fix it.
		r.Log.Error(errors.New("accountId mismatch"),
			"Remote SLI belongs to a different account than spec.accountId; refusing to update",
			"guid", remote.GUID, "remoteAccountId", remote.Events.Account.ID, "specAccountId", sl.Spec.AccountID)
		return ctrl.Result{}, nil
	}

	switch {
	case remote == nil:
		if err := r.createServiceLevel(&sl); err != nil {
			return ctrl.Result{}, err
		}
	case r.matchesRemoteIndicatorSpec(remote, &sl):
		r.Log.Info("SLI matches remote state, adopting with no API call", "guid", remote.GUID)
		sl.Status.ServiceLevelGUID = remote.GUID
		sl.Status.ServiceLevelID = remote.ID
		sl.Status.AppliedSpec = buildAppliedServiceLevelSpec(&sl, remote)
	default:
		if err := r.updateServiceLevel(&sl, remote); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.updateStatusIfChanged(ctx, &sl); err != nil {
		r.Log.Error(err, "failed to update ServiceLevel status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// updateStatusIfChanged writes sl.Status onto the latest version of the
// object on the API server, retrying on conflict. The in-memory sl may have a
// stale ResourceVersion by the time this runs (e.g. a concurrent reconcile
// triggered by the finalizer-add Update already advanced it), so status is
// always applied against a fresh Get rather than the caller's copy.
func (r *ServiceLevelReconciler) updateStatusIfChanged(ctx context.Context, sl *alertsv1.ServiceLevel) error {
	key := types.NamespacedName{Namespace: sl.Namespace, Name: sl.Name}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current alertsv1.ServiceLevel
		if err := r.Get(ctx, key, &current); err != nil {
			return err
		}

		if reflect.DeepEqual(current.Status, sl.Status) {
			return nil
		}

		current.Status = sl.Status
		return r.Status().Update(ctx, &current)
	})
}

func (r *ServiceLevelReconciler) createServiceLevel(sl *alertsv1.ServiceLevel) error {
	r.Log.Info("Creating ServiceLevel", "name", sl.Spec.Name, "entityGuid", sl.Spec.EntityGUID)

	input, err := translateServiceLevelCreateInput(sl)
	if err != nil {
		r.Log.Error(err, "failed to translate ServiceLevel create input", "name", sl.Spec.Name)
		return err
	}

	created, err := r.ServiceLevels.ServiceLevel().ServiceLevelCreate(sl.Spec.EntityGUID, input)
	if err != nil {
		r.Log.Error(err, "failed to create ServiceLevel",
			"name", sl.Spec.Name,
			"entityGuid", sl.Spec.EntityGUID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	sl.Status.ServiceLevelGUID = created.GUID
	sl.Status.ServiceLevelID = created.ID
	sl.Status.AppliedSpec = buildAppliedServiceLevelSpec(sl, created)

	return nil
}

func (r *ServiceLevelReconciler) updateServiceLevel(sl *alertsv1.ServiceLevel, remote *servicelevel.ServiceLevelIndicator) error {
	r.Log.Info("Updating ServiceLevel", "name", sl.Spec.Name, "guid", remote.GUID)

	input, err := translateServiceLevelUpdateInput(sl)
	if err != nil {
		r.Log.Error(err, "failed to translate ServiceLevel update input", "name", sl.Spec.Name)
		return err
	}

	updated, err := r.ServiceLevels.ServiceLevel().ServiceLevelUpdate(remote.GUID, input)
	if err != nil {
		r.Log.Error(err, "failed to update ServiceLevel",
			"name", sl.Spec.Name,
			"guid", remote.GUID,
			"apiKey", interfaces.PartialAPIKey(r.apiKey),
		)
		return err
	}

	sl.Status.ServiceLevelGUID = updated.GUID
	sl.Status.ServiceLevelID = updated.ID
	sl.Status.AppliedSpec = buildAppliedServiceLevelSpec(sl, updated)

	return nil
}

// getExistingIndicator fetches the SLIs attached to spec.EntityGUID and picks
// the one owned by this CR (see findIndicator). GetIndicators returns a
// an error when the entity has zero SLIs and when the GUID
// resolves to nothing
func (r *ServiceLevelReconciler) getExistingIndicator(sl *alertsv1.ServiceLevel) (*servicelevel.ServiceLevelIndicator, error) {
	indicators, err := r.ServiceLevels.ServiceLevel().GetIndicators(sl.Spec.EntityGUID)
	if err != nil {
		var notFound *nrerrors.NotFound
		if errors.As(err, &notFound) {
			r.Log.Info("No SLIs found for entity - creating", "entityGuid", sl.Spec.EntityGUID)
			return nil, nil
		}

		return nil, err
	}

	if indicators == nil {
		return nil, nil
	}

	return r.findIndicator(*indicators, sl), nil
}

// findIndicator matches by stored GUID first, then by name. GUID-first is what
// makes rename work: with name-first, a changed spec.name would find nothing
// and the controller would create a second SLI, orphaning the first. Name
// matching exists only for adoption of a hand-created SLI and for a stale
// stored GUID. On a total miss, the stored GUID is cleared so the reconcile
// falls to create rather than updating a dead GUID.
func (r *ServiceLevelReconciler) findIndicator(indicators []servicelevel.ServiceLevelIndicator, sl *alertsv1.ServiceLevel) *servicelevel.ServiceLevelIndicator {
	if sl.Status.ServiceLevelGUID != "" {
		for i := range indicators {
			if indicators[i].GUID == sl.Status.ServiceLevelGUID {
				return &indicators[i]
			}
		}
		r.Log.Info("Stored ServiceLevel GUID not found among remote SLIs", "guid", sl.Status.ServiceLevelGUID, "entityGuid", sl.Spec.EntityGUID)
	}

	var match *servicelevel.ServiceLevelIndicator
	for i := range indicators {
		if indicators[i].Name != sl.Spec.Name {
			continue
		}
		if match == nil {
			match = &indicators[i]
			continue
		}
		// Duplicate name found, ignore subsequent GUID(s). Only the first result
		// returned is adopted.
		r.Log.Info("Duplicate SLI name found on entity, ignoring subsequent GUID", "name", indicators[i].Name, "ignoredGuid", indicators[i].GUID)
	}

	if match == nil {
		sl.Status.ServiceLevelGUID = ""
		sl.Status.ServiceLevelID = ""
	}

	return match
}

// matchesRemoteIndicatorSpec reports whether sl's desired spec matches the
// remote SLI, after normalizing both sides to the same spec.
func (r *ServiceLevelReconciler) matchesRemoteIndicatorSpec(remote *servicelevel.ServiceLevelIndicator, sl *alertsv1.ServiceLevel) bool {
	desired := normalizeIndicatorSpec(*sl.Spec.ServiceLevelIndicatorSpec.DeepCopy())
	remoteSpec := normalizeIndicatorSpec(buildIndicatorSpecFromRemote(remote))

	return reflect.DeepEqual(desired, remoteSpec)
}

// buildIndicatorSpecFromRemote projects a remote SLI into spec shape. It never
// copies CreatedAt/UpdatedAt/CreatedBy/UpdatedBy/ID/GUID/EntityGUID/Events.Account
// - those change on every write (or belong outside the comparable struct) and
// would guarantee a permanent mismatch if included.
func buildIndicatorSpecFromRemote(remote *servicelevel.ServiceLevelIndicator) alertsv1.ServiceLevelIndicatorSpec {
	spec := alertsv1.ServiceLevelIndicatorSpec{
		Name:        remote.Name,
		Description: remote.Description,
		Events: alertsv1.ServiceLevelEvents{
			ValidEvents: buildEventsQueryFromRemote(remote.Events.ValidEvents),
			GoodEvents:  buildEventsQueryFromRemote(remote.Events.GoodEvents),
			BadEvents:   buildEventsQueryFromRemote(remote.Events.BadEvents),
		},
	}

	for _, obj := range remote.Objectives {
		spec.Objectives = append(spec.Objectives, alertsv1.ServiceLevelObjective{
			Name:        obj.Name,
			Description: obj.Description,
			Target:      strconv.FormatFloat(obj.Target, 'f', -1, 64),
			TimeWindow: alertsv1.ServiceLevelObjectiveTimeWindow{
				Rolling: alertsv1.ServiceLevelObjectiveRollingTimeWindow{
					Count: obj.TimeWindow.Rolling.Count,
					Unit:  obj.TimeWindow.Rolling.Unit,
				},
			},
		})
	}

	return spec
}

func buildEventsQueryFromRemote(remote *servicelevel.ServiceLevelEventsQuery) *alertsv1.ServiceLevelEventsQuery {
	if remote == nil {
		return nil
	}

	query := &alertsv1.ServiceLevelEventsQuery{
		From:  remote.From,
		Where: remote.Where,
		Select: &alertsv1.ServiceLevelEventsQuerySelect{
			Function:  remote.Select.Function,
			Attribute: remote.Select.Attribute,
		},
	}

	// Threshold is a bare float64 on the read type, returning 0 for every
	// non-GET_CDF_COUNT SLI. Blank it unless the function is GET_CDF_COUNT, or it
	// would never match an omitted spec field.
	if remote.Select.Function == servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.GET_CDF_COUNT {
		query.Select.Threshold = strconv.FormatFloat(remote.Select.Threshold, 'f', -1, 64)
	}

	return query
}

// normalizeIndicatorSpec puts a spec into the canonical form used for remote
// comparison. Runs on BOTH the remote projection and a DeepCopy of the desired
// spec, so the two sides cannot disagree about what canonical means.
//
// Objectives is a slice and Events.* are pointers, so a plain struct copy still
// shares backing memory with the input - mutating a nested field here without
// copying it first would corrupt the caller's data (rewrite the live spec, or
// persist normalized junk into appliedSpec). Every nested field is copied
// before being touched.
func normalizeIndicatorSpec(spec alertsv1.ServiceLevelIndicatorSpec) alertsv1.ServiceLevelIndicatorSpec {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description = strings.TrimSpace(spec.Description)

	spec.Events.ValidEvents = normalizeEventsQuery(spec.Events.ValidEvents)
	spec.Events.GoodEvents = normalizeEventsQuery(spec.Events.GoodEvents)
	spec.Events.BadEvents = normalizeEventsQuery(spec.Events.BadEvents)

	if len(spec.Objectives) == 0 {
		// reflect.DeepEqual([]T{}, nil) is false - collapse both to nil.
		spec.Objectives = nil
	} else {
		objectives := make([]alertsv1.ServiceLevelObjective, len(spec.Objectives))
		copy(objectives, spec.Objectives)
		for i := range objectives {
			objectives[i].Name = strings.TrimSpace(objectives[i].Name)
			objectives[i].Description = strings.TrimSpace(objectives[i].Description)
			objectives[i].Target = alertsv1.NormalizeDecimal(objectives[i].Target)
		}
		spec.Objectives = objectives
	}

	return spec
}

// normalizeEventsQuery returns a new *ServiceLevelEventsQuery in canonical
// form. Never mutates the query passed in.
func normalizeEventsQuery(query *alertsv1.ServiceLevelEventsQuery) *alertsv1.ServiceLevelEventsQuery {
	if query == nil {
		return nil
	}

	normalized := *query
	normalized.From = servicelevel.NRQL(alertsv1.NormalizeNRQL(string(query.From)))
	normalized.Where = servicelevel.NRQL(alertsv1.NormalizeNRQL(string(query.Where)))

	if normalized.Select == nil {
		normalized.Select = &alertsv1.ServiceLevelEventsQuerySelect{
			Function: servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.COUNT,
		}
		return &normalized
	}

	sel := *normalized.Select
	if sel.Function == "" {
		sel.Function = servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.COUNT
	}

	// NR drops attribute silently when function is COUNT; blank it defensively
	// so a pre-webhook CR with a stray attribute doesn't loop forever.
	if sel.Function == servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.COUNT {
		sel.Attribute = ""
	}

	if sel.Function == servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.GET_CDF_COUNT {
		sel.Threshold = alertsv1.NormalizeDecimal(sel.Threshold)
	} else {
		sel.Threshold = ""
	}

	normalized.Select = &sel

	return &normalized
}

// snapshotServiceLevelSpec returns a deep copy of spec, safe to store in status
// without aliasing live spec memory - a deep copy is made.
func snapshotServiceLevelSpec(spec alertsv1.ServiceLevelSpec) *alertsv1.ServiceLevelSpec {
	cloned := spec.DeepCopy()
	if cloned == nil {
		return &alertsv1.ServiceLevelSpec{}
	}

	return cloned
}

// buildAppliedServiceLevelSpec snapshots sl.Spec as the value that was actually
// applied, with the indicator fields replaced by what the remote SLI actually
// reflects in the same way matchesRemoteIndicatorSpec would - this
// keeps status.appliedSpec in the same normalized form the equality gate
// expects on the next reconcile.
func buildAppliedServiceLevelSpec(sl *alertsv1.ServiceLevel, remote *servicelevel.ServiceLevelIndicator) *alertsv1.ServiceLevelSpec {
	applied := snapshotServiceLevelSpec(sl.Spec)
	applied.ServiceLevelIndicatorSpec = normalizeIndicatorSpec(buildIndicatorSpecFromRemote(remote))

	return applied
}

func translateServiceLevelCreateInput(sl *alertsv1.ServiceLevel) (servicelevel.ServiceLevelIndicatorCreateInput, error) {
	events, err := translateEventsCreateInput(sl)
	if err != nil {
		return servicelevel.ServiceLevelIndicatorCreateInput{}, err
	}

	objectives, err := translateObjectivesCreateInput(sl.Spec.Objectives)
	if err != nil {
		return servicelevel.ServiceLevelIndicatorCreateInput{}, err
	}

	return servicelevel.ServiceLevelIndicatorCreateInput{
		Name:        sl.Spec.Name,
		Description: sl.Spec.Description,
		Events:      events,
		Objectives:  objectives,
	}, nil
}

func translateEventsCreateInput(sl *alertsv1.ServiceLevel) (servicelevel.ServiceLevelEventsCreateInput, error) {
	input := servicelevel.ServiceLevelEventsCreateInput{
		AccountID: sl.Spec.AccountID,
	}

	var err error

	input.ValidEvents, err = translateEventsQueryCreateInput(sl.Spec.Events.ValidEvents)
	if err != nil {
		return input, err
	}

	input.GoodEvents, err = translateEventsQueryCreateInput(sl.Spec.Events.GoodEvents)
	if err != nil {
		return input, err
	}

	input.BadEvents, err = translateEventsQueryCreateInput(sl.Spec.Events.BadEvents)
	if err != nil {
		return input, err
	}

	return input, nil
}

func translateEventsQueryCreateInput(query *alertsv1.ServiceLevelEventsQuery) (*servicelevel.ServiceLevelEventsQueryCreateInput, error) {
	if query == nil {
		return nil, nil
	}

	input := &servicelevel.ServiceLevelEventsQueryCreateInput{
		From:  query.From,
		Where: query.Where,
	}

	sel, err := translateSelectCreateInput(query.Select)
	if err != nil {
		return nil, err
	}
	input.Select = sel

	return input, nil
}

func translateSelectCreateInput(sel *alertsv1.ServiceLevelEventsQuerySelect) (*servicelevel.ServiceLevelEventsQuerySelectCreateInput, error) {
	if sel == nil {
		return nil, nil
	}

	input := &servicelevel.ServiceLevelEventsQuerySelectCreateInput{
		Function:  sel.Function,
		Attribute: sel.Attribute,
	}

	if sel.Threshold != "" {
		threshold, err := strconv.ParseFloat(sel.Threshold, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid select.threshold %q: %w", sel.Threshold, err)
		}
		input.Threshold = threshold
	}

	return input, nil
}

func translateObjectivesCreateInput(objectives []alertsv1.ServiceLevelObjective) ([]servicelevel.ServiceLevelObjectiveCreateInput, error) {
	result := make([]servicelevel.ServiceLevelObjectiveCreateInput, 0, len(objectives))

	for _, obj := range objectives {
		target, err := strconv.ParseFloat(obj.Target, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid objective target %q: %w", obj.Target, err)
		}

		result = append(result, servicelevel.ServiceLevelObjectiveCreateInput{
			Name:        obj.Name,
			Description: obj.Description,
			Target:      target,
			TimeWindow: servicelevel.ServiceLevelObjectiveTimeWindowCreateInput{
				Rolling: servicelevel.ServiceLevelObjectiveRollingTimeWindowCreateInput{
					Count: obj.TimeWindow.Rolling.Count,
					Unit:  obj.TimeWindow.Rolling.Unit,
				},
			},
		})
	}

	return result, nil
}

func translateServiceLevelUpdateInput(sl *alertsv1.ServiceLevel) (servicelevel.ServiceLevelIndicatorUpdateInput, error) {
	events, err := translateEventsUpdateInput(sl)
	if err != nil {
		return servicelevel.ServiceLevelIndicatorUpdateInput{}, err
	}

	objectives, err := translateObjectivesUpdateInput(sl.Spec.Objectives)
	if err != nil {
		return servicelevel.ServiceLevelIndicatorUpdateInput{}, err
	}

	return servicelevel.ServiceLevelIndicatorUpdateInput{
		Name:        sl.Spec.Name,
		Description: sl.Spec.Description,
		Events:      events,
		Objectives:  objectives,
	}, nil
}

// translateEventsUpdateInput never sets AccountID - ServiceLevelEventsUpdateInput
// has no such field, since the source account is structurally unchangeable
// after create.
func translateEventsUpdateInput(sl *alertsv1.ServiceLevel) (*servicelevel.ServiceLevelEventsUpdateInput, error) {
	input := &servicelevel.ServiceLevelEventsUpdateInput{}

	var err error

	input.ValidEvents, err = translateEventsQueryUpdateInput(sl.Spec.Events.ValidEvents)
	if err != nil {
		return nil, err
	}

	input.GoodEvents, err = translateEventsQueryUpdateInput(sl.Spec.Events.GoodEvents)
	if err != nil {
		return nil, err
	}

	input.BadEvents, err = translateEventsQueryUpdateInput(sl.Spec.Events.BadEvents)
	if err != nil {
		return nil, err
	}

	return input, nil
}

func translateEventsQueryUpdateInput(query *alertsv1.ServiceLevelEventsQuery) (*servicelevel.ServiceLevelEventsQueryUpdateInput, error) {
	if query == nil {
		return nil, nil
	}

	input := &servicelevel.ServiceLevelEventsQueryUpdateInput{
		From:  query.From,
		Where: query.Where,
	}

	sel, err := translateSelectUpdateInput(query.Select)
	if err != nil {
		return nil, err
	}
	input.Select = sel

	return input, nil
}

func translateSelectUpdateInput(sel *alertsv1.ServiceLevelEventsQuerySelect) (*servicelevel.ServiceLevelEventsQuerySelectUpdateInput, error) {
	if sel == nil {
		return nil, nil
	}

	input := &servicelevel.ServiceLevelEventsQuerySelectUpdateInput{
		Function:  sel.Function,
		Attribute: sel.Attribute,
	}

	if sel.Threshold != "" {
		threshold, err := strconv.ParseFloat(sel.Threshold, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid select.threshold %q: %w", sel.Threshold, err)
		}
		input.Threshold = threshold
	}

	return input, nil
}

func translateObjectivesUpdateInput(objectives []alertsv1.ServiceLevelObjective) ([]servicelevel.ServiceLevelObjectiveUpdateInput, error) {
	result := make([]servicelevel.ServiceLevelObjectiveUpdateInput, 0, len(objectives))

	for _, obj := range objectives {
		target, err := strconv.ParseFloat(obj.Target, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid objective target %q: %w", obj.Target, err)
		}

		result = append(result, servicelevel.ServiceLevelObjectiveUpdateInput{
			Name:        obj.Name,
			Description: obj.Description,
			Target:      target,
			TimeWindow: servicelevel.ServiceLevelObjectiveTimeWindowUpdateInput{
				Rolling: servicelevel.ServiceLevelObjectiveRollingTimeWindowUpdateInput{
					Count: obj.TimeWindow.Rolling.Count,
					Unit:  obj.TimeWindow.Rolling.Unit,
				},
			},
		})
	}

	return result, nil
}

// getAPIKeyOrSecret returns an API key or secret to use in client init
func (r *ServiceLevelReconciler) getAPIKeyOrSecret(sl alertsv1.ServiceLevel) (string, error) {
	if sl.Spec.APIKey != "" {
		return sl.Spec.APIKey, nil
	}

	if sl.Spec.APIKeySecret != (alertsv1.NewRelicSecret{}) {
		key := types.NamespacedName{Namespace: sl.Spec.APIKeySecret.Namespace, Name: sl.Spec.APIKeySecret.Name}

		var apiKeySecret v1.Secret
		getErr := r.Client.Get(context.Background(), key, &apiKeySecret)
		if getErr != nil {
			r.Log.Error(getErr, "Failed to retrieve secret", "secret", apiKeySecret)
			return "", getErr
		}

		cleanKey := strings.TrimSpace(string(apiKeySecret.Data[sl.Spec.APIKeySecret.KeyName])) //in case key was encoded with new lines/spaces by accident

		return cleanKey, nil
	}

	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceLevelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&alertsv1.ServiceLevel{}).
		Complete(r)
}
