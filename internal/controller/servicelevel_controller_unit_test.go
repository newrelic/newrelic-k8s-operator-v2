package controller

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/go-logr/logr"
	"github.com/newrelic/newrelic-client-go/v2/pkg/common"
	"github.com/newrelic/newrelic-client-go/v2/pkg/servicelevel"
	alertsv1 "github.com/newrelic/newrelic-k8s-operator-v2/api/v1"
)

func testServiceLevelReconciler() *ServiceLevelReconciler {
	return &ServiceLevelReconciler{Log: logr.Discard()}
}

func testServiceLevel() *alertsv1.ServiceLevel {
	sl := &alertsv1.ServiceLevel{
		Spec: alertsv1.ServiceLevelSpec{
			APIKey:     "api-key",
			Region:     "US",
			AccountID:  12345,
			EntityGUID: "MXxBUE18QVBQTElDQVRJT058MQ",
			ServiceLevelIndicatorSpec: alertsv1.ServiceLevelIndicatorSpec{
				Name:        "Latency",
				Description: "test SLI",
				Events: alertsv1.ServiceLevelEvents{
					ValidEvents: &alertsv1.ServiceLevelEventsQuery{
						From: "Transaction",
					},
					GoodEvents: &alertsv1.ServiceLevelEventsQuery{
						From:  "Transaction",
						Where: "duration < 0.1",
					},
				},
				Objectives: []alertsv1.ServiceLevelObjective{
					{
						Target: "99.9",
						TimeWindow: alertsv1.ServiceLevelObjectiveTimeWindow{
							Rolling: alertsv1.ServiceLevelObjectiveRollingTimeWindow{
								Count: 7,
								Unit:  servicelevel.ServiceLevelObjectiveRollingTimeWindowUnitTypes.DAY,
							},
						},
					},
				},
			},
		},
	}

	sl.ApplyDefaults()

	return sl
}

// testRemoteIndicatorFor builds the *servicelevel.ServiceLevelIndicator that NR
// would return for exactly what sl describes, so matchesRemoteIndicatorSpec
// should consider them equal.
func testRemoteIndicatorFor(sl *alertsv1.ServiceLevel) *servicelevel.ServiceLevelIndicator {
	remote := &servicelevel.ServiceLevelIndicator{
		Name:        sl.Spec.Name,
		Description: sl.Spec.Description,
		GUID:        common.EntityGUID("sli-guid"),
		EntityGUID:  sl.Spec.EntityGUID,
		Events: servicelevel.ServiceLevelEvents{
			ValidEvents: remoteQueryFor(sl.Spec.Events.ValidEvents),
			GoodEvents:  remoteQueryFor(sl.Spec.Events.GoodEvents),
			BadEvents:   remoteQueryFor(sl.Spec.Events.BadEvents),
		},
	}

	for _, obj := range sl.Spec.Objectives {
		target, _ := strconv.ParseFloat(obj.Target, 64) //nolint:errcheck // test fixture, input is controlled

		remote.Objectives = append(remote.Objectives, servicelevel.ServiceLevelObjective{
			Name:        obj.Name,
			Description: obj.Description,
			Target:      target,
			TimeWindow: servicelevel.ServiceLevelObjectiveTimeWindow{
				Rolling: servicelevel.ServiceLevelObjectiveRollingTimeWindow{
					Count: obj.TimeWindow.Rolling.Count,
					Unit:  obj.TimeWindow.Rolling.Unit,
				},
			},
		})
	}

	return remote
}

func remoteQueryFor(query *alertsv1.ServiceLevelEventsQuery) *servicelevel.ServiceLevelEventsQuery {
	if query == nil {
		return nil
	}

	remote := &servicelevel.ServiceLevelEventsQuery{
		From:  query.From,
		Where: query.Where,
	}

	if query.Select != nil {
		remote.Select = servicelevel.ServiceLevelEventsQuerySelect{
			Function:  query.Select.Function,
			Attribute: query.Select.Attribute,
		}

		if query.Select.Function == servicelevel.ServiceLevelEventsQuerySelectFunctionTypes.GET_CDF_COUNT && query.Select.Threshold != "" {
			threshold, _ := strconv.ParseFloat(query.Select.Threshold, 64) //nolint:errcheck // test fixture, input is controlled
			remote.Select.Threshold = threshold
		}
	}

	return remote
}

func TestMatchesRemoteIndicatorSpecRoundTrip(t *testing.T) {
	sl := testServiceLevel()
	remote := testRemoteIndicatorFor(sl)

	r := testServiceLevelReconciler()

	if !r.matchesRemoteIndicatorSpec(remote, sl) {
		t.Fatalf("expected a defaulted spec to match its own remote projection")
	}
}

func TestMatchesRemoteIndicatorSpecDoesNotMutateSpec(t *testing.T) {
	sl := testServiceLevel()
	remote := testRemoteIndicatorFor(sl)
	original := sl.Spec.DeepCopy()

	r := testServiceLevelReconciler()

	r.matchesRemoteIndicatorSpec(remote, sl)
	r.matchesRemoteIndicatorSpec(remote, sl)

	if !reflect.DeepEqual(*original, sl.Spec) {
		t.Fatalf("expected matchesRemoteIndicatorSpec to leave sl.Spec unchanged, got:\nwant: %+v\ngot:  %+v", *original, sl.Spec)
	}
}

func TestNormalizeDecimal(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"99", "99"},
		{"99.0", "99"},
		{"99.00000", "99"},
		{"9.9e1", "99"},
	}

	for _, c := range cases {
		if got := alertsv1.NormalizeDecimal(c.input); got != c.want {
			t.Errorf("NormalizeDecimal(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFindIndicator(t *testing.T) {
	r := testServiceLevelReconciler()

	guidA := common.EntityGUID("sli-guid-a")
	guidB := common.EntityGUID("sli-guid-b")

	t.Run("first reconcile, no remote SLI", func(t *testing.T) {
		sl := &alertsv1.ServiceLevel{Spec: alertsv1.ServiceLevelSpec{ServiceLevelIndicatorSpec: alertsv1.ServiceLevelIndicatorSpec{Name: "Latency"}}}

		if got := r.findIndicator(nil, sl); got != nil {
			t.Fatalf("expected no match, got %+v", got)
		}
	})

	t.Run("first reconcile, SLI exists - adopt by name", func(t *testing.T) {
		sl := &alertsv1.ServiceLevel{Spec: alertsv1.ServiceLevelSpec{ServiceLevelIndicatorSpec: alertsv1.ServiceLevelIndicatorSpec{Name: "Latency"}}}
		indicators := []servicelevel.ServiceLevelIndicator{{GUID: guidA, Name: "Latency"}}

		got := r.findIndicator(indicators, sl)
		if got == nil || got.GUID != guidA {
			t.Fatalf("expected match by name on guidA, got %+v", got)
		}
	})

	t.Run("spec.name changed, GUID still resolves - GUID wins over a same-name SLI elsewhere", func(t *testing.T) {
		sl := &alertsv1.ServiceLevel{
			Spec:   alertsv1.ServiceLevelSpec{ServiceLevelIndicatorSpec: alertsv1.ServiceLevelIndicatorSpec{Name: "New Name"}},
			Status: alertsv1.ServiceLevelStatus{ServiceLevelGUID: guidA},
		}
		indicators := []servicelevel.ServiceLevelIndicator{
			{GUID: guidA, Name: "Old Name"},
			{GUID: guidB, Name: "New Name"},
		}

		got := r.findIndicator(indicators, sl)
		if got == nil || got.GUID != guidA {
			t.Fatalf("expected the stored GUID match to win over a name match on a different SLI, got %+v", got)
		}
	})

	t.Run("GUID stored, SLI deleted out-of-band - GUID cleared, falls to create", func(t *testing.T) {
		sl := &alertsv1.ServiceLevel{
			Spec:   alertsv1.ServiceLevelSpec{ServiceLevelIndicatorSpec: alertsv1.ServiceLevelIndicatorSpec{Name: "Latency"}},
			Status: alertsv1.ServiceLevelStatus{ServiceLevelGUID: guidA, ServiceLevelID: "old-id"},
		}
		indicators := []servicelevel.ServiceLevelIndicator{{GUID: guidB, Name: "Unrelated"}}

		got := r.findIndicator(indicators, sl)
		if got != nil {
			t.Fatalf("expected no match, got %+v", got)
		}
		if sl.Status.ServiceLevelGUID != "" || sl.Status.ServiceLevelID != "" {
			t.Fatalf("expected stale GUID/ID to be cleared, got guid=%q id=%q", sl.Status.ServiceLevelGUID, sl.Status.ServiceLevelID)
		}
	})
}
