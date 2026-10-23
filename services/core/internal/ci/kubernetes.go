package ci

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubernetesClient usa soltanto le API autorizzate al dispatcher nel namespace
// dei job. La costruzione in-cluster evita di rendere disponibili credenziali
// Kubernetes ai pod che eseguono i workflow.
type KubernetesClient struct {
	client kubernetes.Interface
}

func NewKubernetesClient() (*KubernetesClient, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("configurazione Kubernetes in-cluster: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("client Kubernetes: %w", err)
	}
	return &KubernetesClient{client: client}, nil
}

// Create traduce il modello indipendente dal cluster nel pod restricted.
func (k *KubernetesClient) Create(ctx context.Context, p Pod) error {
	if k == nil || k.client == nil {
		return fmt.Errorf("client Kubernetes non configurato")
	}
	pod, err := corePod(p)
	if err != nil {
		return err
	}
	_, err = k.client.CoreV1().Pods(p.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	return err
}

// Wait osserva il pod e ricontrolla con Get: un watch puo' chiudersi fra due
// eventi senza che il dispatcher perda la conclusione.
func (k *KubernetesClient) Wait(ctx context.Context, namespace, name string) (string, error) {
	if k == nil || k.client == nil {
		return "", fmt.Errorf("client Kubernetes non configurato")
	}
	if conclusion, done := k.getConclusion(ctx, namespace, name); done {
		return conclusion, nil
	}
	w, err := k.client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
	})
	if err != nil {
		return "", err
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-w.ResultChan():
			if !ok {
				if conclusion, done := k.getConclusion(ctx, namespace, name); done {
					return conclusion, nil
				}
				return "", fmt.Errorf("watch del pod %s chiuso", name)
			}
			if event.Type == watch.Error {
				return "", fmt.Errorf("watch del pod %s: %v", name, event.Object)
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if conclusion, done := podConclusion(pod); done {
				return conclusion, nil
			}
		}
	}
}

// Delete elimina il pod dopo che il suo esito e' stato registrato.
func (k *KubernetesClient) Delete(ctx context.Context, namespace, name string) error {
	if k == nil || k.client == nil {
		return fmt.Errorf("client Kubernetes non configurato")
	}
	err := k.client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubernetesClient) Exists(ctx context.Context, namespace, name string) (bool, error) {
	if k == nil || k.client == nil {
		return false, fmt.Errorf("client Kubernetes non configurato")
	}
	_, err := k.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (k *KubernetesClient) getConclusion(ctx context.Context, namespace, name string) (string, bool) {
	pod, err := k.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ConclusionFailure, true
	}
	if err != nil {
		return "", false
	}
	return podConclusion(pod)
}

func podConclusion(pod *corev1.Pod) (string, bool) {
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return ConclusionSuccess, true
	case corev1.PodFailed:
		if pod.Status.Reason == "DeadlineExceeded" {
			return ConclusionTimedOut, true
		}
		return ConclusionFailure, true
	}
	return "", false
}

func corePod(p Pod) (*corev1.Pod, error) {
	resources, err := resourceList(p)
	if err != nil {
		return nil, err
	}
	noToken := false
	noPrivilege := false
	readOnly := p.ReadOnlyRootFilesystem
	runAsNonRoot := true
	mode := int64(10001)
	if p.RunAsUser != 0 {
		mode = p.RunAsUser
	}
	security := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &noPrivilege,
		ReadOnlyRootFilesystem:   &readOnly,
		RunAsNonRoot:             &runAsNonRoot,
		RunAsUser:                &mode,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	container := corev1.Container{Name: "job", Image: p.Image, Command: p.Command, Env: podEnv(p.Env),
		Resources: corev1.ResourceRequirements{Requests: resources, Limits: resources}, SecurityContext: security,
		VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/work"}, {Name: "tmp", MountPath: "/tmp"}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Labels: map[string]string{"gitstack.io/ci-job": "true"}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: &noToken,
			EnableServiceLinks: ptr(false),
			PriorityClassName:  p.PriorityClass, ActiveDeadlineSeconds: &p.ActiveDeadlineSeconds,
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &runAsNonRoot, RunAsUser: &mode,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, Containers: []corev1.Container{container},
			Volumes: []corev1.Volume{{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr(resources[corev1.ResourceEphemeralStorage])}}}, {Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr(resources[corev1.ResourceEphemeralStorage])}}}}}}
	if p.InitGate {
		pod.Spec.InitContainers = []corev1.Container{{Name: "gate", Image: p.Image,
			Command: []string{"/usr/local/bin/gitstack-ci-gate"}, Resources: corev1.ResourceRequirements{Requests: resources, Limits: resources}, SecurityContext: security,
			VolumeMounts: []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}}}
	}
	return pod, nil
}

func ptr[T any](v T) *T { return &v }

func quantityPtr(q resource.Quantity) *resource.Quantity { return &q }

func resourceList(p Pod) (corev1.ResourceList, error) {
	parse := func(value, name string) (resource.Quantity, error) {
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return resource.Quantity{}, fmt.Errorf("risorsa %s: %w", name, err)
		}
		return q, nil
	}
	cpu, err := parse(p.CPU, "cpu")
	if err != nil {
		return nil, err
	}
	memory, err := parse(p.Memory, "memory")
	if err != nil {
		return nil, err
	}
	disk, err := parse(p.EphemeralStorage, "ephemeral-storage")
	if err != nil {
		return nil, err
	}
	return corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: memory, corev1.ResourceEphemeralStorage: disk}, nil
}

func podEnv(values map[string]string) []corev1.EnvVar {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]corev1.EnvVar, 0, len(keys))
	for _, key := range keys {
		result = append(result, corev1.EnvVar{Name: key, Value: values[key]})
	}
	return result
}
