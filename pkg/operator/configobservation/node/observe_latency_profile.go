package node

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configV1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

var nodeMonitorGracePeriodPath = []string{"extendedArguments", "node-monitor-grace-period"}

// ObserveNodeMonitorGracePeriod observes the value that should be set for node-monitor-grace-period
// controller manager argument on the basis of provided worker latency profile from config node object.
func ObserveNodeMonitorGracePeriod(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains node-monitor-grace-period field.
		ret = configobserver.Pruned(ret, nodeMonitorGracePeriodPath)
	}()

	nodeLister := genericListers.(configobservation.Listers).NodeLister
	configNode, err := nodeLister.Get("cluster")
	if err != nil && !apierrors.IsNotFound(err) {
		// we got an error so without the node object we are not able to determine worker latency profile
		return existingConfig, append(errs, err)
	} else if apierrors.IsNotFound(err) {
		// if config/v1/node/cluster object is not found, that can be treated as a non-error case
		return existingConfig, errs
	}

	// read the observed value
	var observedNodeMonitorGracePeriod string
	switch configNode.Spec.WorkerLatencyProfile {
	case configV1.DefaultUpdateDefaultReaction:
		observedNodeMonitorGracePeriod = configV1.DefaultNodeMonitorGracePeriod.String()
	case configV1.MediumUpdateAverageReaction:
		observedNodeMonitorGracePeriod = configV1.MediumNodeMonitorGracePeriod.String()
	case configV1.LowUpdateSlowReaction:
		observedNodeMonitorGracePeriod = configV1.LowNodeMonitorGracePeriod.String()
	// in case of empty worker latency profile, do not update config
	case "":
		return existingConfig, errs
	default:
		return existingConfig, append(errs, fmt.Errorf("unknown worker latency profile found: %v", configNode.Spec.WorkerLatencyProfile))
	}

	// read the current value
	var currentNodeMonitorGracePeriod string
	currentNodeMonitorGracePeriodSlice, _, err := unstructured.NestedStringSlice(
		existingConfig, nodeMonitorGracePeriodPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract node monitor grace period from the existing config: %v", err))
		// keep going, we are only interested in the observed value which will overwrite the current configuration anyway
	}
	if len(currentNodeMonitorGracePeriodSlice) > 0 {
		currentNodeMonitorGracePeriod = currentNodeMonitorGracePeriodSlice[0]
	}

	// see if the current and the observed value differ
	observedConfig := map[string]interface{}{}
	if currentNodeMonitorGracePeriod != observedNodeMonitorGracePeriod {
		if err = unstructured.SetNestedStringSlice(observedConfig,
			[]string{observedNodeMonitorGracePeriod},
			nodeMonitorGracePeriodPath...); err != nil {
			return existingConfig, append(errs, err)
		}
		return observedConfig, errs
	}
	// nothing has changed return the original configuration
	return existingConfig, errs
}
