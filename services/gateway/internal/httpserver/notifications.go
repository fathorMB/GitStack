package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Notifiche, iscrizioni, Watch, preferenze email e webhook (M-06, GIT-129):
// servite da core, il gateway le instrada con l'handler condiviso (le
// dichiarazioni di sicurezza vengono dal contratto).

func (s *apiServer) DeleteNotifications(w http.ResponseWriter, r *http.Request, _ openapi.DeleteNotificationsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListNotifications(w http.ResponseWriter, r *http.Request, _ openapi.ListNotificationsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) MarkAllNotificationsRead(w http.ResponseWriter, r *http.Request, _ openapi.MarkAllNotificationsReadParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteNotification(w http.ResponseWriter, r *http.Request, _ openapi.NotificationIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetNotification(w http.ResponseWriter, r *http.Request, _ openapi.NotificationIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateNotification(w http.ResponseWriter, r *http.Request, _ openapi.NotificationIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListOrgWebhooks(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.ListOrgWebhooksParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateOrgWebhook(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteOrgWebhook(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetOrgWebhook(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateOrgWebhook(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListOrgWebhookDeliveries(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam, _ openapi.ListOrgWebhookDeliveriesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetOrgWebhookDelivery(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) RedeliverOrgWebhookDelivery(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ReactivateOrgWebhook(w http.ResponseWriter, r *http.Request, _ openapi.OrgParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListRepoWebhooks(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListRepoWebhooksParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateRepoWebhook(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteRepoWebhook(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepoWebhook(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateRepoWebhook(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListRepoWebhookDeliveries(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam, _ openapi.ListRepoWebhookDeliveriesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepoWebhookDelivery(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) RedeliverRepoWebhookDelivery(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam, _ openapi.WebhookDeliveryIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ReactivateRepoWebhook(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.WebhookIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UnsubscribeIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetIssueSubscription(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SubscribeIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ResetRepoWatch(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepoWatch(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SetRepoWatch(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}
