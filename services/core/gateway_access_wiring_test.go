package main

import "testing"

func TestGatewayAccessFrom(t *testing.T) {
	env := map[string]string{
		envAppsGatewayNamespace: "gitstack", envAppsGatewayService: "gitstack-gateway",
		envAppsGatewayPodLabels: "app.kubernetes.io/name=gateway, app.kubernetes.io/instance=gitstack", envAppsGatewayPort: "8081",
	}
	look := func(k string) string { return env[k] }
	g := gatewayAccessFrom(look)
	if g == nil || g.Port != 8081 || g.Service != "gitstack-gateway" || g.PodLabels["app.kubernetes.io/instance"] != "gitstack" || len(g.PodLabels) != 2 {
		t.Fatalf("configurazione letta male: %+v", g)
	}
	for _, k := range []string{envAppsGatewayNamespace, envAppsGatewayService, envAppsGatewayPodLabels, envAppsGatewayPort} {
		saved := env[k]
		env[k] = ""
		if gatewayAccessFrom(look) != nil {
			t.Errorf("senza %s la configurazione deve valere come assente", k)
		}
		env[k] = saved
	}
}
