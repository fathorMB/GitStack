package apps

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-356 (H3): con i volumi l'aggiornamento è Recreate e i PVC sono ReadWriteOnce su local-path.
func TestBuildDeployment_ConVolumiRecreateEMount(t *testing.T) {
	app, ver := testApp()
	ver.Config.Copies = 1
	ver.Config.Volumes = []store.AppVolumeSpec{
		{Name: "data", MountPath: "/var/lib/data", SizeBytes: 1 << 30},
		{Name: "uploads", MountPath: "/srv/uploads", SizeBytes: 512 << 20},
	}
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 1})
	if d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || d.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("strategia con volumi: %+v", d.Spec.Strategy)
	}
	pod := d.Spec.Template.Spec
	if len(pod.Volumes) != 2 || pod.Volumes[0].PersistentVolumeClaim == nil || pod.Volumes[0].PersistentVolumeClaim.ClaimName != "vol-data" {
		t.Fatalf("volumi del pod: %+v", pod.Volumes)
	}
	mounts := pod.Containers[0].VolumeMounts
	if len(mounts) != 2 || mounts[0].Name != pod.Volumes[0].Name || mounts[0].MountPath != "/var/lib/data" || mounts[1].MountPath != "/srv/uploads" {
		t.Fatalf("mount: %+v", mounts)
	}
}

func TestBuildDeployment_SenzaVolumiResteRollingUpdate(t *testing.T) {
	app, ver := testApp()
	d := BuildDeployment(app, ver, BuildOptions{Replicas: 2})
	if d.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType || d.Spec.Strategy.RollingUpdate == nil {
		t.Fatalf("strategia senza volumi: %+v", d.Spec.Strategy)
	}
	if len(d.Spec.Template.Spec.Volumes) != 0 || len(d.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 {
		t.Fatalf("senza volumi nessun mount")
	}
}

func TestBuildPVC(t *testing.T) {
	app, _ := testApp()
	p := BuildPVC(app, store.AppVolumeSpec{Name: "data", MountPath: "/d", SizeBytes: 2 << 30})
	if p.Name != "vol-data" || p.Namespace != Namespace(app.ID) || p.Labels[LabelManaged] != "true" || p.Labels[LabelVolume] != "data" {
		t.Fatalf("metadati: %s %s %v", p.Name, p.Namespace, p.Labels)
	}
	if p.Spec.StorageClassName == nil || *p.Spec.StorageClassName != "local-path" {
		t.Fatalf("storage class: %v", p.Spec.StorageClassName)
	}
	if len(p.Spec.AccessModes) != 1 || p.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Fatalf("access modes: %v", p.Spec.AccessModes)
	}
	if q := p.Spec.Resources.Requests[corev1.ResourceStorage]; q.Value() != 2<<30 {
		t.Fatalf("dimensione: %s", q.String())
	}
}

func TestVolumeKubeName_NomiDelPod(t *testing.T) {
	if volumeKubeName("data") != "vol-data" || PVCName("data") != "vol-data" {
		t.Fatal("nome del PVC e del volume del pod")
	}
}
