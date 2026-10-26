package apps

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// ADR 0002: i permessi sull'app esistono solo nel namespace dell'app, con un RoleBinding verso il
// ClusterRole modello; mai un ClusterRoleBinding.
func TestEnsureRoleBinding_NelNamespaceDellApp(t *testing.T) {
	kube := fake.NewSimpleClientset()
	c := New(nil, kube, nil, Config{ControllerServiceAccount: "gitstack/gitstack-core", WorkloadRole: "gitstack-app-workload"})
	for i := 0; i < 2; i++ { // idempotente
		if err := c.ensureRoleBinding(context.Background(), "gitstack-app-x"); err != nil {
			t.Fatal(err)
		}
	}
	rb, err := kube.RbacV1().RoleBindings("gitstack-app-x").Get(context.Background(), RoleBindingName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rb.RoleRef.Kind != "ClusterRole" || rb.RoleRef.Name != "gitstack-app-workload" {
		t.Fatalf("roleRef inatteso: %+v", rb.RoleRef)
	}
	if len(rb.Subjects) != 1 || rb.Subjects[0].Name != "gitstack-core" || rb.Subjects[0].Namespace != "gitstack" {
		t.Fatalf("subject inatteso: %+v", rb.Subjects)
	}
	crbs, _ := kube.RbacV1().ClusterRoleBindings().List(context.Background(), metav1.ListOptions{})
	if len(crbs.Items) != 0 {
		t.Fatalf("nessun ClusterRoleBinding per le app, trovati %d", len(crbs.Items))
	}
}

func TestEnsureRoleBinding_SenzaConfigurazioneNonFaNiente(t *testing.T) {
	kube := fake.NewSimpleClientset()
	c := New(nil, kube, nil, Config{})
	if err := c.ensureRoleBinding(context.Background(), "gitstack-app-x"); err != nil {
		t.Fatal(err)
	}
	if len(kube.Actions()) != 0 {
		t.Fatalf("attese zero chiamate, trovate %v", kube.Actions())
	}
}
