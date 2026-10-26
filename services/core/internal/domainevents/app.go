package domainevents

import "encoding/json"

// Eventi delle app (v3-a, GIT-384, H12). Il deploy produce un evento per ogni azione del webhook
// deployment (started, succeeded, failed, rolled_back); lo stato «fermata» è app_status.unhealthy:
// un'app che era in esecuzione e ha le copie non pronte. Le istantanee (app e versione) sono
// composte da core nella transazione della modifica, come per le esecuzioni della CI: la consegna
// non rilegge nulla.
const (
	AppDeploymentStarted    = "app_deployment.started"
	AppDeploymentSucceeded  = "app_deployment.succeeded"
	AppDeploymentFailed     = "app_deployment.failed"
	AppDeploymentRolledBack = "app_deployment.rolled_back"

	AppStatusUnhealthy = "app_status.unhealthy"
)

// AppDeploymentPayload è lo schema interno di app_deployment.*. App e Deployment sono gli oggetti
// App e AppVersion del contratto al momento dell'evento (WebhookDeploymentPayload). Actor è chi ha
// fatto il deploy (un agente ha la sua identità). Reason è il motivo del fallimento, vuoto altrove.
type AppDeploymentPayload struct {
	Repo       Repo            `json:"repo"`
	Actor      *User           `json:"actor"`
	AppName    string          `json:"appName"`
	Version    int             `json:"version"`
	Reason     string          `json:"reason,omitempty"`
	App        json.RawMessage `json:"app"`
	Deployment json.RawMessage `json:"deployment"`
}

// AppStatusPayload è lo schema interno di app_status.unhealthy. Actor è chi ha fatto l'ultimo deploy
// attivo: a lui va l'avviso. App è l'istantanea dell'app.
type AppStatusPayload struct {
	Repo    Repo            `json:"repo"`
	Actor   *User           `json:"actor"`
	AppName string          `json:"appName"`
	Reason  string          `json:"reason,omitempty"`
	App     json.RawMessage `json:"app"`
}
