// Copyright 2022 EasyStack, Inc.
package controller

import (
	"context"
	"time"
	"fmt"
)

const (
	NETATTDEFFINALIZERNAME = "plan.finalizers.eks.io"

	AnsiblePlanStartEvent        = "AnsiblePlanStart"
	AnsiblePlanCreatedEvent      = "AnsiblePlanCreated"
	AnsiblePlanStatusUpdateEvent = "AnsiblePlanUpdated"
	AnsiblePlanDeleteEvent       = "AnsiblePlanDeleted"
	AnsiblePlanDeleteSshKeyEvent = "AnsiblePlanDeleteSshKey"

	PlanStartEvent        = "PlanStart"
	PlanCreatedEvent      = "PlanCreated"
	PlanDeleteEvent       = "PlanDeleted"
	PlanDeleteSshKeyEvent = "AnsiblePlanDeleteSshKey"
)

func StringInArray(val string, array []string) bool {
	for i := range array {
		if array[i] == val {
			return true
		}
	}
	return false
}

func RemoveString(s string, slice []string) (result []string, found bool) {
	if len(slice) != 0 {
		for _, item := range slice {
			if item == s {
				found = true
				continue
			}
			result = append(result, item)
		}
	}
	return
}


func Retry(ctx context.Context, maxRetryTime int, interval time.Duration, operation func() error) error {
	var err error
	for attempt := 1; attempt <= maxRetryTime; attempt++ {
		if err := operation(); err == nil {
			return nil
		}

		if attempt < maxRetryTime {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}

	return fmt.Errorf("max retry attempts reached, %s", err.Error())
}