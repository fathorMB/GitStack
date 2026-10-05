package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Notifiche, iscrizioni, Watch, preferenze email e webhook (M-06, GIT-129):
// il contratto (api/openapi.yaml) e lo schema (migrazione 0006) sono fissati,
// le operazioni rispondono 501 finché gli item a valle (M-06/B-G) non le
// implementano. Regole C1-C9: .prisma/knowledge/topics/collegamenti-notifiche-webhook.md.
func notificationsNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Notifiche e webhook non ancora disponibili.")
}

func (s *apiServer) DeleteNotifications(w http.ResponseWriter, _ *http.Request, _ openapi.DeleteNotificationsParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ListNotifications(w http.ResponseWriter, _ *http.Request, _ openapi.ListNotificationsParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) MarkAllNotificationsRead(w http.ResponseWriter, _ *http.Request, _ openapi.MarkAllNotificationsReadParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) DeleteNotification(w http.ResponseWriter, _ *http.Request, _ openapi.NotificationIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetNotification(w http.ResponseWriter, _ *http.Request, _ openapi.NotificationIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) UpdateNotification(w http.ResponseWriter, _ *http.Request, _ openapi.NotificationIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ListOrgWebhooks(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.ListOrgWebhooksParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) CreateOrgWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) DeleteOrgWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetOrgWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) UpdateOrgWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ListOrgWebhookDeliveries(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam, _ openapi.ListOrgWebhookDeliveriesParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetOrgWebhookDelivery(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) RedeliverOrgWebhookDelivery(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ReactivateOrgWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ListRepoWebhooks(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepoWebhooksParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) CreateRepoWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) DeleteRepoWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetRepoWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) UpdateRepoWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ListRepoWebhookDeliveries(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam, _ openapi.ListRepoWebhookDeliveriesParams) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetRepoWebhookDelivery(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) RedeliverRepoWebhookDelivery(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ReactivateRepoWebhook(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) UnsubscribeIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetIssueSubscription(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) SubscribeIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) ResetRepoWatch(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetRepoWatch(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) SetRepoWatch(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	notificationsNotImplemented(w)
}

func (s *apiServer) GetNotificationPreferences(w http.ResponseWriter, _ *http.Request) {
	notificationsNotImplemented(w)
}

func (s *apiServer) UpdateNotificationPreferences(w http.ResponseWriter, _ *http.Request) {
	notificationsNotImplemented(w)
}
