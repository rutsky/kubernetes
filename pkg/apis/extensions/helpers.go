/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package extensions

import (
	"k8s.io/kubernetes/pkg/api"
)

// ScaleFromDeployment returns a scale subresource for a deployment.
func ScaleFromDeployment(deployment *Deployment) *Scale {
	return &Scale{
		ObjectMeta: api.ObjectMeta{
			Name:              deployment.Name,
			Namespace:         deployment.Namespace,
			CreationTimestamp: deployment.CreationTimestamp,
		},
		Spec: ScaleSpec{
			Replicas: deployment.Spec.Replicas,
		},
		Status: ScaleStatus{
			Replicas: deployment.Status.Replicas,
			Selector: deployment.Spec.Selector,
		},
	}
}
