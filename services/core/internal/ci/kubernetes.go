package ci

// KubernetesClient è l'adattatore che l'avvio di core configura con il client
// in-cluster. Le sue due operazioni sono intenzionalmente piccole: il Role del
// chart concede create/get/list/watch/delete pods e pods/log, nulla fuori dal
// namespace dei job.
type KubernetesClient struct{}
