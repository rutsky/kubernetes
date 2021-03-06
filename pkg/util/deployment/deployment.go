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

package deployment

import (
	"fmt"
	"strconv"
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/extensions"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/release_1_2"
	"k8s.io/kubernetes/pkg/labels"
	labelsutil "k8s.io/kubernetes/pkg/util/labels"
	podutil "k8s.io/kubernetes/pkg/util/pod"
)

const (
	// The revision annotation of a deployment's replication controllers which records its rollout sequence
	RevisionAnnotation = "deployment.kubernetes.io/revision"

	// Here are the possible rollback event reasons
	RollbackRevisionNotFound  = "DeploymentRollbackRevisionNotFound"
	RollbackTemplateUnchanged = "DeploymentRollbackTemplateUnchanged"
	RollbackDone              = "DeploymentRollback"
)

// GetOldRCs returns the old RCs targeted by the given Deployment; get PodList and RCList from client interface.
// Note that the first set of old RCs doesn't include the ones with no pods, and the second set of old RCs include all old RCs.
func GetOldRCs(deployment extensions.Deployment, c clientset.Interface) ([]*api.ReplicationController, []*api.ReplicationController, error) {
	return GetOldRCsFromLists(deployment, c,
		func(namespace string, options api.ListOptions) (*api.PodList, error) {
			return c.Core().Pods(namespace).List(options)
		},
		func(namespace string, options api.ListOptions) ([]api.ReplicationController, error) {
			rcList, err := c.Core().ReplicationControllers(namespace).List(options)
			return rcList.Items, err
		})
}

// GetOldRCsFromLists returns two sets of old RCs targeted by the given Deployment; get PodList and RCList with input functions.
// Note that the first set of old RCs doesn't include the ones with no pods, and the second set of old RCs include all old RCs.
func GetOldRCsFromLists(deployment extensions.Deployment, c clientset.Interface, getPodList func(string, api.ListOptions) (*api.PodList, error), getRcList func(string, api.ListOptions) ([]api.ReplicationController, error)) ([]*api.ReplicationController, []*api.ReplicationController, error) {
	namespace := deployment.ObjectMeta.Namespace
	// 1. Find all pods whose labels match deployment.Spec.Selector
	selector := labels.SelectorFromSet(deployment.Spec.Selector)
	options := api.ListOptions{LabelSelector: selector}
	podList, err := getPodList(namespace, options)
	if err != nil {
		return nil, nil, fmt.Errorf("error listing pods: %v", err)
	}
	// 2. Find the corresponding RCs for pods in podList.
	// TODO: Right now we list all RCs and then filter. We should add an API for this.
	oldRCs := map[string]api.ReplicationController{}
	allOldRCs := map[string]api.ReplicationController{}
	rcList, err := getRcList(namespace, options)
	if err != nil {
		return nil, nil, fmt.Errorf("error listing replication controllers: %v", err)
	}
	newRCTemplate := GetNewRCTemplate(deployment)
	for _, pod := range podList.Items {
		podLabelsSelector := labels.Set(pod.ObjectMeta.Labels)
		for _, rc := range rcList {
			rcLabelsSelector := labels.SelectorFromSet(rc.Spec.Selector)
			// Filter out RC that has the same pod template spec as the deployment - that is the new RC.
			if api.Semantic.DeepEqual(rc.Spec.Template, &newRCTemplate) {
				continue
			}
			allOldRCs[rc.ObjectMeta.Name] = rc
			if rcLabelsSelector.Matches(podLabelsSelector) {
				oldRCs[rc.ObjectMeta.Name] = rc
			}
		}
	}
	requiredRCs := []*api.ReplicationController{}
	for key := range oldRCs {
		value := oldRCs[key]
		requiredRCs = append(requiredRCs, &value)
	}
	allRCs := []*api.ReplicationController{}
	for key := range allOldRCs {
		value := allOldRCs[key]
		allRCs = append(allRCs, &value)
	}
	return requiredRCs, allRCs, nil
}

// GetNewRC returns an RC that matches the intent of the given deployment; get RCList from client interface.
// Returns nil if the new RC doesnt exist yet.
func GetNewRC(deployment extensions.Deployment, c clientset.Interface) (*api.ReplicationController, error) {
	return GetNewRCFromList(deployment, c,
		func(namespace string, options api.ListOptions) ([]api.ReplicationController, error) {
			rcList, err := c.Core().ReplicationControllers(namespace).List(options)
			return rcList.Items, err
		})
}

// GetNewRCFromList returns an RC that matches the intent of the given deployment; get RCList with the input function.
// Returns nil if the new RC doesnt exist yet.
func GetNewRCFromList(deployment extensions.Deployment, c clientset.Interface, getRcList func(string, api.ListOptions) ([]api.ReplicationController, error)) (*api.ReplicationController, error) {
	namespace := deployment.ObjectMeta.Namespace
	rcList, err := getRcList(namespace, api.ListOptions{LabelSelector: labels.SelectorFromSet(deployment.Spec.Selector)})
	if err != nil {
		return nil, fmt.Errorf("error listing replication controllers: %v", err)
	}
	newRCTemplate := GetNewRCTemplate(deployment)

	for i := range rcList {
		if api.Semantic.DeepEqual(rcList[i].Spec.Template, &newRCTemplate) {
			// This is the new RC.
			return &rcList[i], nil
		}
	}
	// new RC does not exist.
	return nil, nil
}

// Returns the desired PodTemplateSpec for the new RC corresponding to the given RC.
func GetNewRCTemplate(deployment extensions.Deployment) api.PodTemplateSpec {
	// newRC will have the same template as in deployment spec, plus a unique label in some cases.
	newRCTemplate := api.PodTemplateSpec{
		ObjectMeta: deployment.Spec.Template.ObjectMeta,
		Spec:       deployment.Spec.Template.Spec,
	}
	newRCTemplate.ObjectMeta.Labels = labelsutil.CloneAndAddLabel(
		deployment.Spec.Template.ObjectMeta.Labels,
		deployment.Spec.UniqueLabelKey,
		podutil.GetPodTemplateSpecHash(newRCTemplate))
	return newRCTemplate
}

// SetTemplate sets the desired PodTemplateSpec from an RC template to the given deployment.
func SetFromRCTemplate(deployment *extensions.Deployment, template api.PodTemplateSpec) *extensions.Deployment {
	deployment.Spec.Template.ObjectMeta = template.ObjectMeta
	deployment.Spec.Template.Spec = template.Spec
	deployment.Spec.Template.ObjectMeta.Labels = labelsutil.CloneAndRemoveLabel(
		deployment.Spec.Template.ObjectMeta.Labels,
		deployment.Spec.UniqueLabelKey)
	return deployment
}

// Returns the sum of Replicas of the given replication controllers.
func GetReplicaCountForRCs(replicationControllers []*api.ReplicationController) int {
	totalReplicaCount := 0
	for _, rc := range replicationControllers {
		totalReplicaCount += rc.Spec.Replicas
	}
	return totalReplicaCount
}

// Returns the number of available pods corresponding to the given RCs.
func GetAvailablePodsForRCs(c clientset.Interface, rcs []*api.ReplicationController, minReadySeconds int) (int, error) {
	allPods, err := getPodsForRCs(c, rcs)
	if err != nil {
		return 0, err
	}
	return getReadyPodsCount(allPods, minReadySeconds), nil
}

func getReadyPodsCount(pods []api.Pod, minReadySeconds int) int {
	readyPodCount := 0
	for _, pod := range pods {
		if api.IsPodReady(&pod) {
			// Check if we've passed minReadySeconds since LastTransitionTime
			// If so, this pod is ready
			for _, c := range pod.Status.Conditions {
				// we only care about pod ready conditions
				if c.Type == api.PodReady {
					// 2 cases that this ready condition is valid (passed minReadySeconds, i.e. the pod is ready):
					// 1. minReadySeconds <= 0
					// 2. LastTransitionTime (is set) + minReadySeconds (>0) < current time
					minReadySecondsDuration := time.Duration(minReadySeconds) * time.Second
					if minReadySeconds <= 0 || !c.LastTransitionTime.IsZero() && c.LastTransitionTime.Add(minReadySecondsDuration).Before(time.Now()) {
						readyPodCount++
						break
					}
				}
			}
		}
	}
	return readyPodCount
}

func getPodsForRCs(c clientset.Interface, replicationControllers []*api.ReplicationController) ([]api.Pod, error) {
	allPods := []api.Pod{}
	for _, rc := range replicationControllers {
		selector := labels.SelectorFromSet(rc.Spec.Selector)
		options := api.ListOptions{LabelSelector: selector}
		podList, err := c.Core().Pods(rc.ObjectMeta.Namespace).List(options)
		if err != nil {
			return allPods, fmt.Errorf("error listing pods: %v", err)
		}
		allPods = append(allPods, podList.Items...)
	}
	return allPods, nil
}

// Revision returns the revision number of the input RC
func Revision(rc *api.ReplicationController) (int64, error) {
	v, ok := rc.Annotations[RevisionAnnotation]
	if !ok {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}
