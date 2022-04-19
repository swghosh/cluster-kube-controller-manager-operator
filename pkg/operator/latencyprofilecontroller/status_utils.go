package latencyprofilecontroller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const (
	reasonLatencyProfileUpdateTriggered = "ProfileUpdateTriggered"
	reasonLatencyProfileUpdated         = "ProfileUpdated"
	reasonLatencyProfileEmpty           = "ProfileEmpty"
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