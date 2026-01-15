// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package status

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	applymetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// ConditionApplyConfigs takes a set of existing conditions and conditions of a
// desired state and merges them idempotently to create a series of ConditionApplyConfiguration
// values that can be used in a server-side apply.
func ConditionApplyConfigs(existing []metav1.Condition, generation int64, conditions []metav1.Condition) []*applymetav1.ConditionApplyConfiguration {
	now := metav1.Now()
	configurations := []*applymetav1.ConditionApplyConfiguration{}

	for _, condition := range conditions {
		existingCondition := apimeta.FindStatusCondition(existing, condition.Type)
		if existingCondition == nil {
			configurations = append(configurations, conditionToConfig(generation, now, condition))
			continue
		}

		if existingCondition.Status != condition.Status {
			configurations = append(configurations, conditionToConfig(generation, now, condition))
			continue
		}

		if existingCondition.Reason != condition.Reason {
			configurations = append(configurations, conditionToConfig(generation, now, condition))
			continue
		}

		if existingCondition.Message != condition.Message {
			configurations = append(configurations, conditionToConfig(generation, now, condition))
			continue
		}

		if existingCondition.ObservedGeneration != generation {
			configurations = append(configurations, conditionToConfig(generation, now, condition))
			continue
		}

		configurations = append(configurations, conditionToConfig(generation, existingCondition.LastTransitionTime, *existingCondition))
	}

	return configurations
}

func conditionToConfig(generation int64, now metav1.Time, condition metav1.Condition) *applymetav1.ConditionApplyConfiguration {
	return applymetav1.Condition().
		WithType(condition.Type).
		WithStatus(condition.Status).
		WithReason(condition.Reason).
		WithObservedGeneration(generation).
		WithLastTransitionTime(now).
		WithMessage(condition.Message)
}
