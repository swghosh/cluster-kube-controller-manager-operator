package node

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

type workerLatencyProfileTestScenario struct {
	name                   string
	existingConfig         map[string]interface{}
	expectedObservedConfig map[string]interface{}
	workerLatencyProfile   configv1.WorkerLatencyProfileType
	expectedErrorContents  string
}

func TestObserveNodeMonitorGracePeriod(t *testing.T) {
	scenarios := []workerLatencyProfileTestScenario{
		// scenario 1: empty worker latency profile
		{
			name:                   "default value is not applied when worker latency profile is unset",
			expectedObservedConfig: nil,
			workerLatencyProfile:   "", // empty worker latency profile
		},

		// scenario 2: Default
		{
			name: "worker latency profile Default: config with node-monitor-grace-period=40s",
			expectedObservedConfig: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"node-monitor-grace-period": []interface{}{"40s"},
				},
			},
			workerLatencyProfile: configv1.DefaultUpdateDefaultReaction,
		},

		// scenario 3: MediumUpdateAverageReaction
		{
			name: "worker latency profile MediumUpdateAverageReaction: config with node-monitor-grace-period=2m",
			expectedObservedConfig: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"node-monitor-grace-period": []interface{}{"2m0s"},
				},
			},
			workerLatencyProfile: configv1.MediumUpdateAverageReaction,
		},

		// scenario 4: LowUpdateSlowReaction
		{
			name: "worker latency profile LowUpdateSlowReaction: config with node-monitor-grace-period=5m",
			expectedObservedConfig: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"node-monitor-grace-period": []interface{}{"5m0s"},
				},
			},
			workerLatencyProfile: configv1.LowUpdateSlowReaction,
		},

		// scenario 5: unknown worker latency profile
		{
			name: "unknown worker latency profile should raise error but retain existing config",

			// in this case where we encounter an unknown value for WorkerLatencyProfile,
			// existing config should the same as expected config, because in case
			// an invalid profile is found we'd like to stick to whatever was set last time
			// and not update any config to avoid breaking anything
			existingConfig: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"node-monitor-grace-period": []interface{}{"40s"},
				},
			},
			expectedObservedConfig: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"node-monitor-grace-period": []interface{}{"40s"},
				},
			},

			workerLatencyProfile:  "UnknownProfile",
			expectedErrorContents: "unknown worker latency profile",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("")
			configNodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			configNodeIndexer.Add(&configv1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       configv1.NodeSpec{WorkerLatencyProfile: scenario.workerLatencyProfile},
			})
			listers := configobservation.Listers{
				NodeLister: configlistersv1.NewNodeLister(configNodeIndexer),
			}

			// act
			observedKubeAPIConfig, err := ObserveNodeMonitorGracePeriod(listers, eventRecorder, scenario.existingConfig)

			// validate
			if scenario.expectedErrorContents != "" {
				// expect an error to occur in this case
				errStr := v1helpers.NewMultiLineAggregate(err).Error() // concat multiple errors together into single string

				// check if error message contains required string
				if !strings.Contains(errStr, scenario.expectedErrorContents) {
					t.Fatalf("expected error with message = %v, found error = %v", scenario.expectedErrorContents, errStr)
				}
			} else {
				// expect no error in this case
				if len(err) > 0 {
					t.Fatal(err)
				}
			}

			// compare value of expectedObservedConfig and actualObservedConfig
			if !cmp.Equal(scenario.expectedObservedConfig, observedKubeAPIConfig) {
				t.Fatalf("unexpected configuration, diff = %v", cmp.Diff(scenario.expectedObservedConfig, observedKubeAPIConfig))
			}
		})
	}
}
