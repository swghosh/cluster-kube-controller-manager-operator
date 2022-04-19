package latencyprofilecontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	apiconfigv1 "github.com/openshift/api/config/v1"
	controlplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	listerv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

const (
	controllerManagerConfigMapName = "config"
	controllerManagerConfigMapKey  = "config.yaml"

	nodeMonitorGracePeriodArgument = "node-monitor-grace-period"
)

// LatencyProfileController either instantly via the informer
// or periodically via resync, lists the config/v1/node object
// and fetches the worker latency profile applied on the cluster which is used to
// updates the status of the config node object. The status updates reflect the
// state of kube-controller-manager(s) running on control plane node(s) and their
// observed config for node-monitor-grace-period match the applied arguments.
type LatencyProfileController struct {
	operatorClient  v1helpers.StaticPodOperatorClient
	configClient    configv1.ConfigV1Interface
	configMapClient corev1client.ConfigMapsGetter
	nodeLister      listerv1.NodeLister
}

func NewLatencyProfileController(
	operatorClient v1helpers.StaticPodOperatorClient,
	configClient configv1.ConfigV1Interface,
	nodeInformer configv1informers.NodeInformer,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) factory.Controller {

	ret := &LatencyProfileController{
		operatorClient:  operatorClient,
		configClient:    configClient,
		configMapClient: v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		nodeLister:      nodeInformer.Lister(),
	}

	return factory.New().WithInformers(
		// this is for our general configuration input and our status output in case another actor changes it
		operatorClient.Informer(),

		// We use nodeInformer for observing current worker latency profile
		nodeInformer.Informer(),

		// for configmaps of operator client target namespace
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer(),
	).ResyncEvery(5*time.Minute).WithSync(ret.sync).ToController(
		"LatencyProfileController",
		eventRecorder.WithComponentSuffix("latency-profile-controller"),
	)
}

func (c *LatencyProfileController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	syncErr := c.updateLatencyProfileSyncedStatus(ctx)

	condition := operatorv1.OperatorCondition{
		Type:   "LatencyProfileControllerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if syncErr != nil {
		condition.Status = operatorv1.ConditionTrue
		condition.Reason = "Error"
		condition.Message = syncErr.Error()
	}
	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(condition)); updateErr != nil {
		return updateErr
	}
	return syncErr
}

// updateLatencyProfileSyncedStatus checks if the given worker latency profile have synced
// their respective kcm arguments to all control plane nodes in the cluster and updates
// workerlatencyprofile status in config node object
func (c *LatencyProfileController) updateLatencyProfileSyncedStatus(ctx context.Context) error {
	// Collect the current config node object, if present
	configNodeObj, err := c.nodeLister.Get("cluster")
	if err != nil && !errors.IsNotFound(err) {
		return err
	} else if errors.IsNotFound(err) {
		// if config/v1/node/cluster object is not found this controller should do nothing
		return nil
	}

	degradedCondition := metav1.Condition{
		Type:   apiconfigv1.KubeControllerManagerDegraded,
		Status: metav1.ConditionUnknown,
	}
	progressingCondition := metav1.Condition{
		Type:   apiconfigv1.KubeControllerManagerProgressing,
		Status: metav1.ConditionUnknown,
	}
	completedCondition := metav1.Condition{
		Type:   apiconfigv1.KubeControllerManagerComplete,
		Status: metav1.ConditionUnknown,
	}
	// do nothing if workerLatencyProfile is not set
	if configNodeObj.Spec.WorkerLatencyProfile == "" {
		// TODO: in case worker latency profile is transitioned
		// from Default/Medium/Low to ""
		// check that the required extendedArgs should vanish

		degradedCondition.Status = metav1.ConditionFalse
		progressingCondition.Status = metav1.ConditionFalse
		completedCondition.Status = metav1.ConditionFalse

		degradedCondition.Reason = reasonLatencyProfileEmpty
		progressingCondition.Reason = reasonLatencyProfileEmpty
		completedCondition.Reason = reasonLatencyProfileEmpty

		degradedCondition.Message = "worker latency profile not set on cluster"

		_, err := c.updateConfigNodeStatus(ctx, degradedCondition, progressingCondition, completedCondition)
		return err
	}

	desiredControllerManagerArgumentVals := map[string]string{}
	switch configNodeObj.Spec.WorkerLatencyProfile {
	case apiconfigv1.DefaultUpdateDefaultReaction:
		desiredControllerManagerArgumentVals[nodeMonitorGracePeriodArgument] = apiconfigv1.DefaultNodeMonitorGracePeriod.String()
	case apiconfigv1.MediumUpdateAverageReaction:
		desiredControllerManagerArgumentVals[nodeMonitorGracePeriodArgument] = apiconfigv1.MediumNodeMonitorGracePeriod.String()
	case apiconfigv1.LowUpdateSlowReaction:
		desiredControllerManagerArgumentVals[nodeMonitorGracePeriodArgument] = apiconfigv1.LowNodeMonitorGracePeriod.String()
	}

	_, operatorStatus, _, err := c.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return err
	}

	// Collect the unique set of revisions of the node static pods
	revisionMap := map[int32]struct{}{}
	uniqueRevisions := []int32{}
	for _, nodeStatus := range operatorStatus.NodeStatuses {
		revision := nodeStatus.CurrentRevision
		if _, ok := revisionMap[revision]; !ok {
			revisionMap[revision] = struct{}{}
			uniqueRevisions = append(uniqueRevisions, revision)
		}
	}

	// For each revision, check that the configmap for that revision have
	// correct argument values or not
	revisionsHaveSynced := true
	for _, revision := range uniqueRevisions {
		configMapNameWithRevision := fmt.Sprintf("%s-%d", controllerManagerConfigMapName, revision)
		configMap, err := c.configMapClient.ConfigMaps(operatorclient.TargetNamespace).Get(ctx, configMapNameWithRevision, metav1.GetOptions{})
		if err != nil {
			return err
		}
		match, err := configMatchesControllerManagerArguments(configMap, desiredControllerManagerArgumentVals)
		if err != nil {
			return err
		}
		if !match {
			revisionsHaveSynced = false
			break
		}
	}

	if revisionsHaveSynced {
		// Controller Manager has Completed rollout
		completedCondition.Status = metav1.ConditionTrue
		completedCondition.Message = "all kube-controller-manager(s) have updated latency profile"
		completedCondition.Reason = reasonLatencyProfileUpdated

		// Controller Manager is not Progressing rollout
		progressingCondition.Status = metav1.ConditionFalse
		progressingCondition.Reason = reasonLatencyProfileUpdated

		// Controller Manager is not Degraded
		degradedCondition.Status = metav1.ConditionFalse
		degradedCondition.Reason = reasonLatencyProfileUpdated
	} else {
		// Controller Manager has not Completed rollout
		completedCondition.Status = metav1.ConditionFalse
		completedCondition.Reason = reasonLatencyProfileUpdateTriggered

		// Controller Manager is Progressing rollout
		progressingCondition.Status = metav1.ConditionTrue
		progressingCondition.Message = "kube-controller-manager(s) are updating latency profile"
		progressingCondition.Reason = reasonLatencyProfileUpdateTriggered

		// Controller Manager is not Degraded
		degradedCondition.Status = metav1.ConditionFalse
		degradedCondition.Reason = reasonLatencyProfileUpdateTriggered
	}
	_, err = c.updateConfigNodeStatus(ctx, degradedCondition, progressingCondition, completedCondition)
	return err
}

// configHasControllerManagerArguments checks if the specified config map containing kcm node config
// contains the specified argument and value in observedconfig.extendedarguments field
func configMatchesControllerManagerArguments(configMap *corev1.ConfigMap, argValMap map[string]string) (bool, error) {
	configData, ok := configMap.Data[controllerManagerConfigMapKey]
	if !ok {
		return false, fmt.Errorf("could not find %s in %s config map from %s namespace", controllerManagerConfigMapKey, configMap.Name, configMap.Namespace)
	}
	var kubeControllerManagerConfig controlplanev1.KubeControllerManagerConfig
	if err := json.Unmarshal([]byte(configData), &kubeControllerManagerConfig); err != nil {
		return false, err
	}

	for arg := range argValMap {
		expectedValue := argValMap[arg]
		extendedArgumentFetchedValues, ok := kubeControllerManagerConfig.ExtendedArguments[arg]

		// such an argument does not exist in config
		if !ok {
			return false, nil
		}
		if len(extendedArgumentFetchedValues) > 0 {
			if !(extendedArgumentFetchedValues[0] == expectedValue) {
				return false, nil
			}
		}
	}
	return true, nil
}
