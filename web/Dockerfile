# syntax=docker/dockerfile:1
#
# Immagine della web UI di GitStack (React + TypeScript, SPA, M-01 T-07).
# Contesto di build: la RADICE del monorepo, non web/ da sola, perché il
# progetto dipende in locale da client/ts (client TypeScript generato dal
# contratto OpenAPI, distribuito come dipendenza di pacchetto "link:",
# non pubblicato su un registry npm — vedi client/README.md e
# web/package.json):
#
#   docker build -f web/Dockerfile .
#
# Il tag e il push nel registry interno li fa il job `registry` (GIT-2), con
# lo stesso schema di gateway e core.

# --- Stage 1: build --------------------------------------------------------
FROM node:22-alpine AS build

RUN corepack enable
WORKDIR /src

# client/ts e i soli manifesti di web/ prima del resto del codice: un
# livello di cache che cambia solo quando cambiano le dipendenze, non a ogni
# modifica della UI.
COPY client/ts client/ts
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/
WORKDIR /src/web
RUN pnpm install --frozen-lockfile

WORKDIR /src
COPY web web
WORKDIR /src/web
RUN pnpm run build

# --- Stage 2: runtime -------------------------------------------------------
# Immagine nginx ufficiale già pensata per girare senza privilegi
# (nginxinc/nginx-unprivileged: utente "nginx", porta >1024, nessuna
# directory che richieda root in scrittura): stesso principio dei binari
# distroless "nonroot" di gateway e core, adattato a un server statico.
FROM nginxinc/nginx-unprivileged:1.27-alpine

COPY web/deploy/nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /src/web/dist /usr/share/nginx/html

# Sovrascritto dal chart Helm di GIT-8 con l'indirizzo del Service k3s del
# gateway; qui solo un default utile per `docker run` locale.
ENV GATEWAY_UPSTREAM=http://gateway:8080
# Attiva /docker-entrypoint.d/15-local-resolvers.envsh (immagine base): serve
# NGINX_LOCAL_RESOLVERS al resolver del template (vedi deploy/nginx.conf.template).
ENV NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1

EXPOSE 8080
