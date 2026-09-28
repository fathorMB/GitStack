# Ambiente di sviluppo locale riproducibile (M-01/T-10, GIT-10).
#
# Riusa lo stesso chart Helm (deploy/gitstack) e la stessa sequenza del job
# "chart" di .github/workflows/ci.yml: crea un cluster k3d (un k3s vero in
# Docker, Traefik e le sue CRD già incluse), costruisce le immagini in
# locale, le importa nel cluster con `k3d image import` e installa il chart
# con `helm upgrade --install`. Nessun secondo chart, nessun manifest
# parallelo: le immagini sono le stesse gateway/core/web di GIT-4/GIT-5/GIT-7.
#
# Richiede bash: su Windows usa Git Bash o WSL2 (`make` su cmd.exe/PowerShell
# non è supportato). Guida completa, prerequisiti per Linux/macOS/Windows e
# il ciclo modifica -> rebuild -> redeploy: docs/dev-environment.md.
#
# Target pensati per l'uso interattivo da riga di comando:
#   make dev-up                    crea il cluster (se manca) e installa/aggiorna GitStack
#   make dev-down                  distrugge il cluster
#   make dev-redeploy SVC=gateway  ricostruisce e ridistribuisce un solo servizio (gateway|core|web)
#   make dev-status                pod e ingress del rilascio corrente

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

CLUSTER_NAME ?= gitstack-dev
RELEASE      ?= gitstack
IMAGE_TAG    ?= dev
OWNER        ?= fathormb
REGISTRY     ?= ghcr.io

# Versioni pinnate di k3d e k3s. Il job "chart" di ci.yml non le pinna
# esplicitamente (installa k3d dal branch main del suo installer e non passa
# --image a `k3d cluster create`): k3d, senza --image, risolve il k3s da
# usare interrogando a runtime il canale "latest" di update.k3s.io, quindi
# la versione realmente installata da quel job cambia nel tempo (verificato:
# k3d v5.9.0 in locale ha creato un nodo k3s v1.35.5-k3s1, non quello
# imbarcato "di default" nel binario). Per la riproducibilità richiesta da
# questo item pinniamo qui sia k3d sia l'immagine k3s con --image, invece di
# lasciarli flottanti: rancher/k3s:v1.32.5-k3s1 (>= 1.28, quindi Traefik v3 —
# gruppo CRD "traefik.io", richiesto dal Middleware in
# deploy/gitstack/templates/ingress.yaml), esistente su Docker Hub
# (verificato con l'API di hub.docker.com).
K3D_VERSION  ?= v5.9.0
K3S_IMAGE    ?= rancher/k3s:v1.32.5-k3s1
HELM_VERSION ?= v3.16.3

# Servizi con un'immagine costruibile in locale (identity e git non hanno
# ancora un Dockerfile: arrivano con milestone successive a M-01, come nel
# job "registry" di ci.yml). contesto/Dockerfile identici alla matrice di
# quel job.
DEV_SERVICES := gateway core web

CONTEXT_gateway    := services/gateway
DOCKERFILE_gateway := services/gateway/Dockerfile
CONTEXT_core       := services/core
DOCKERFILE_core    := services/core/Dockerfile
# web: contesto la radice del monorepo (dipende in locale da client/ts, vedi
# web/Dockerfile), non web/ da sola — stesso motivo del job "registry".
CONTEXT_web        := .
DOCKERFILE_web     := web/Dockerfile

IMAGE_gateway := $(REGISTRY)/$(OWNER)/gitstack-gateway:$(IMAGE_TAG)
IMAGE_core    := $(REGISTRY)/$(OWNER)/gitstack-core:$(IMAGE_TAG)
IMAGE_web     := $(REGISTRY)/$(OWNER)/gitstack-web:$(IMAGE_TAG)

.PHONY: help dev-up dev-down dev-redeploy dev-status \
        _dev-check-tools _dev-cluster _dev-build-all _dev-import-all _dev-helm \
        _dev-build _dev-import

help:
	@echo "Target disponibili (guida completa: docs/dev-environment.md):"
	@echo "  make dev-up                     crea/aggiorna il cluster locale k3d + GitStack"
	@echo "  make dev-down                   distrugge il cluster locale"
	@echo "  make dev-redeploy SVC=<servizio> ricostruisce e ridistribuisce un solo servizio ($(DEV_SERVICES))"
	@echo "  make dev-status                 pod e ingress del rilascio corrente"

dev-up: _dev-check-tools _dev-cluster _dev-build-all _dev-import-all _dev-helm
	@echo "==> GitStack pronto sul cluster '$(CLUSTER_NAME)'."
	@echo "==> Ingress Traefik su http://localhost:8080 (es. curl http://localhost:8080/api/healthz)."

dev-down: _dev-check-tools
	@echo "==> Distruggo il cluster k3d '$(CLUSTER_NAME)'..."
	@k3d cluster delete $(CLUSTER_NAME)

dev-redeploy: _dev-check-tools
	@if [ -z "$${SVC:-}" ]; then \
		echo "Uso: make dev-redeploy SVC=<servizio>  (uno tra: $(DEV_SERVICES))" >&2; \
		exit 1; \
	fi
	@case " $(DEV_SERVICES) " in \
		*" $(SVC) "*) ;; \
		*) echo "SVC deve essere uno tra: $(DEV_SERVICES) (ricevuto '$(SVC)')" >&2; exit 1;; \
	esac
	@$(MAKE) --no-print-directory _dev-build SVC=$(SVC)
	@$(MAKE) --no-print-directory _dev-import SVC=$(SVC)
	@echo "==> Riavvio deployment/$(RELEASE)-$(SVC) con l'immagine appena costruita..."
	@kubectl rollout restart deployment/$(RELEASE)-$(SVC)
	@kubectl rollout status deployment/$(RELEASE)-$(SVC) --timeout=120s

dev-status:
	@kubectl get pods -o wide
	@echo
	@kubectl get ingress

# --- passi interni, riusati da dev-up e dev-redeploy -----------------------

_dev-check-tools:
	@command -v docker >/dev/null 2>&1 || { echo "docker non trovato nel PATH: vedi docs/dev-environment.md (prerequisiti)." >&2; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { echo "kubectl non trovato nel PATH: vedi docs/dev-environment.md (prerequisiti)." >&2; exit 1; }
	@if ! command -v helm >/dev/null 2>&1; then echo "helm non trovato nel PATH: vedi docs/dev-environment.md (prerequisiti)." >&2; exit 1; fi
	@if ! command -v k3d >/dev/null 2>&1; then \
		echo "==> k3d non trovato, installo la versione pinnata $(K3D_VERSION) (stesso installer del job 'chart' di ci.yml, versione fissata qui)..."; \
		curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | TAG=$(K3D_VERSION) bash; \
	fi
	@installed="$$(k3d version | awk '/^k3d version/{print $$3}')"; \
	if [ "$$installed" != "$(K3D_VERSION)" ]; then \
		echo "ATTENZIONE: k3d installato è $$installed, questo repo è verificato con $(K3D_VERSION). Continuo comunque; per allinearti: curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | TAG=$(K3D_VERSION) bash" >&2; \
	fi

_dev-cluster:
	@if k3d cluster list -o json 2>/dev/null | grep -q "\"name\": *\"$(CLUSTER_NAME)\""; then \
		echo "==> Cluster k3d '$(CLUSTER_NAME)' già presente, lo riuso."; \
	else \
		echo "==> Creo il cluster k3d '$(CLUSTER_NAME)' (k3s $(K3S_IMAGE))..."; \
		k3d cluster create $(CLUSTER_NAME) --image $(K3S_IMAGE) --port "8080:80@loadbalancer" --wait --timeout 120s; \
	fi

_dev-build-all:
	@for svc in $(DEV_SERVICES); do $(MAKE) --no-print-directory _dev-build SVC=$$svc; done

_dev-import-all:
	@echo "==> Importo le immagini nel cluster k3d '$(CLUSTER_NAME)'..."
	@k3d image import $(IMAGE_gateway) $(IMAGE_core) $(IMAGE_web) -c $(CLUSTER_NAME)

_dev-helm:
	@echo "==> helm upgrade --install $(RELEASE) deploy/gitstack (tag immagine: $(IMAGE_TAG))..."
	@helm upgrade --install $(RELEASE) deploy/gitstack \
		--set global.image.registry=$(REGISTRY) \
		--set global.image.tag=$(IMAGE_TAG) \
		--wait --timeout 3m
	@kubectl rollout status deployment/$(RELEASE)-gateway --timeout=120s
	@kubectl rollout status deployment/$(RELEASE)-core --timeout=120s
	@kubectl rollout status deployment/$(RELEASE)-web --timeout=120s

# _dev-build/_dev-import prendono SVC da riga di comando (es. SVC=gateway):
# usati sia dai target "-all" sia da dev-redeploy per un solo servizio.
_dev-build:
	@echo "==> Costruisco $(IMAGE_$(SVC)) (contesto $(CONTEXT_$(SVC)), $(DOCKERFILE_$(SVC)))..."
	@docker build -f $(DOCKERFILE_$(SVC)) -t $(IMAGE_$(SVC)) $(CONTEXT_$(SVC))

_dev-import:
	@k3d image import $(IMAGE_$(SVC)) -c $(CLUSTER_NAME)
