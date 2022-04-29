package latencyprofilecontroller

import (
	"context"
	"time"

	apiconfigv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const (
	reasonLatencyProfileUpdateTriggered = "ProfileUpdateTriggered"
	reasonLatencyProfileUpdated         = "ProfileUpdated"
	reasonLatencyProfileEmpty           = "ProfileEmpty"

	wlpPrefix = "WorkerLatencyProfile"
)

// setWLPStatusCondition is used to set condition in config node object status.workerLatencyProfileStatus
func setWLPStatusCondition(conditions *[]metav1.Condition, newCondition metav1.Condition) {
	if conditions == nil {
		conditions = &[]metav1.Condition{}
	}
	existingCondition := findWLPStatusCondition(*conditions, newCondition.Type)
	if existingCondition == nil {
		newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		*conditions = append(*conditions, newCondition)
		return
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.Status = newCondition.Status
		existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
	}

	existingCondition.Reason = newCondition.Reason
	existingCondition.Message = newCondition.Message
}

// FindWLPStatusCondition is used to find conditions from config node object status.workerLatencyProfileStatus
func findWLPStatusCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func (c *LatencyProfileController) updateConfigNodeStatus(ctx context.Context, newConditions ...metav1.Condition) (updated bool, err error) {
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		configNodeObj, err := c.nodeLister.Get("cluster")
		if err != nil {
			updated = false
			return err
		}
		oldStatus := configNodeObj.Status

		newStatus := oldStatus.DeepCopy()
		for _, newCondition := range newConditions {
			setWLPStatusCondition(&newStatus.WorkerLatencyProfileStatus.Conditions, newCondition)
		}

		if equality.Semantic.DeepEqual(oldStatus, newStatus) {
			updated = false
			// We return the newStatus which is a deep copy of oldStatus but with set condition applied.
			return nil
		}

		configNodeObj.Status = *newStatus
		_, err = c.configClient.Nodes().UpdateStatus(ctx, configNodeObj, metav1.UpdateOptions{})
		updated = err == nil
		return err
	})

	return updated, err
}

func (c *LatencyProfileController) alternateUpdateStatus(ctx context.Context, newConditions ...operatorv1.OperatorCondition) (updated bool, err error) {
	updateFuncs := make([]v1helpers.UpdateStatusFunc, len(newConditions))
	for i, newCondition := range newConditions {
		updateFuncs[i] = v1helpers.UpdateConditionFn(newCondition)
	}
	_, updated, err = v1helpers.UpdateStatus(ctx, c.operatorClient, updateFuncs...)
	return updated, err
}

func copyConditions(conditions ...metav1.Condition) []operatorv1.OperatorCondition {
	operatorTypes := map[string]string{
		apiconfigv1.KubeControllerManagerComplete:    wlpPrefix + "Complete",
		apiconfigv1.KubeControllerManagerDegraded:    wlpPrefix + operatorv1.OperatorStatusTypeDegraded,
		apiconfigv1.KubeControllerManagerProgressing: wlpPrefix + operatorv1.OperatorStatusTypeProgressing,
	}

	operatorConditions := make([]operatorv1.OperatorCondition, len(conditions))
	for i, condition := range conditions {
		operatorConditions[i] = operatorv1.OperatorCondition{
			Type:    operatorTypes[condition.Type],
			Status:  operatorv1.ConditionStatus(condition.Status),
			Message: condition.Message,
			Reason:  condition.Reason,
		}
	}
	return operatorConditions
}
