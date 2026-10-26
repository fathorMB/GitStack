package ci

import (
	"fmt"
	"strings"
)

// Job di build (ADR 0001 §6, CI2, GIT-371). Un job con la label BuildLabel in runs-on gira in un
// pod del namespace di build: il runner di sempre (act in modalità host, gate fail-closed) più
// BuildKit rootless come sidecar nativo, con il socket in un emptyDir condiviso. Nessun demone
// del nodo, nessun socket del runtime, nessun hostPath. Il job pubblica nel registry del repo
// col proprio token, passando dal gateway nel cluster (mai dall'ingress).
const (
	// BuildLabel è l'etichetta di runs-on che chiede il pod di build.
	BuildLabel = "gitstack-build"

	// BuildKitSocketDir è la cartella (emptyDir) con il socket di buildkitd, condivisa dai due container.
	BuildKitSocketDir = "/run/buildkit"
	// BuildKitAddr è l'indirizzo che buildctl legge da BUILDKIT_HOST.
	BuildKitAddr = "unix://" + BuildKitSocketDir + "/buildkitd.sock"

	// BuildUser è l'uid del pod di build: quello dell'immagine rootless di BuildKit (user 1000),
	// così il runner può usare il socket creato dal sidecar.
	BuildUser = 1000

	// Il namespace di build ha una quota di 2 vCPU, 4 GiB e 20 GiB di disco per pod (per posto
	// di capacity): runner e sidecar la dividono, e non la superano mai insieme.
	BuildRunnerCPU       = "500m"
	BuildRunnerMemory    = "512Mi"
	BuildRunnerEphemeral = "2Gi"
	BuildKitCPU          = "1500m"
	BuildKitMemory       = "3584Mi"
	BuildKitEphemeral    = "18Gi"
)

// IsBuildJob dice se le etichette di runs-on chiedono il pod di build.
func IsBuildJob(runsOn []string) bool {
	for _, l := range NormalizeLabels(runsOn) {
		if l == BuildLabel {
			return true
		}
	}
	return false
}

// BuildKit descrive il sidecar buildkitd rootless del pod di build.
type BuildKit struct {
	Image                         string
	CPU, Memory, EphemeralStorage string
}

// podForBuild trasforma il pod del job in quello di build nel namespace di build.
func podForBuild(c RunnerConfig, p Pod) (Pod, error) {
	if strings.TrimSpace(c.BuildNamespace) == "" || strings.TrimSpace(c.BuildImage) == "" || strings.TrimSpace(c.RegistryAddr) == "" {
		return Pod{}, fmt.Errorf("job %s: build non configurata (ci.build.enabled)", BuildLabel)
	}
	p.Namespace = c.BuildNamespace
	p.RunAsUser = BuildUser
	p.CPU, p.Memory, p.EphemeralStorage = BuildRunnerCPU, BuildRunnerMemory, BuildRunnerEphemeral
	p.BuildKit = &BuildKit{Image: c.BuildImage, CPU: BuildKitCPU, Memory: BuildKitMemory, EphemeralStorage: BuildKitEphemeral}
	// Senza prefisso GITSTACK_CI_: act lo toglie dall'ambiente dei passi (withoutCIEnv) e i passi
	// del workflow devono invece vederlo. Sono indirizzi, non segreti.
	p.Env["BUILDKIT_HOST"] = BuildKitAddr
	p.Env["GITSTACK_REGISTRY"] = c.RegistryAddr
	return p, nil
}
