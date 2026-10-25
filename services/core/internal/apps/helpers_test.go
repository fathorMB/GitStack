package apps

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func fakeDynamic() dynamic.Interface {
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
}

func corev1ContainerStatus(reason string, restarts, exit int32) corev1.Pod {
	cs := corev1.ContainerStatus{RestartCount: restarts, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}}
	if exit != 0 {
		cs.LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exit, Reason: "Error"}}
	}
	return corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}}}
}
