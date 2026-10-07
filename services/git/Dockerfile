# syntax=docker/dockerfile:1
#
# Immagine del servizio git. Contesto di build: la RADICE del monorepo
# (GIT-184); i moduli interni (pkg/*) si compilano dalle sorgenti locali.
#
#   docker build -f services/git/Dockerfile .
#
# Il tag e il push nel registry li fa il job `registry` di ci.yml.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

# Contesto di build: la RADICE del monorepo (GIT-184). I moduli interni
# (pkg/*, client/go) si compilano dalle sorgenti locali del commit, non dalla
# pseudo-versione del go.mod: scripts/docker-local-replace.sh scrive i replace.
#   docker build -f services/git/Dockerfile .
WORKDIR /src
COPY pkg ./pkg
COPY scripts/docker-local-replace.sh ./scripts/docker-local-replace.sh

WORKDIR /src/services/git

# Livello di cache separato per i moduli di terze parti.
COPY services/git/go.mod services/git/go.sum ./
RUN sh /src/scripts/docker-local-replace.sh /src \
    && for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY services/git/ .

RUN sh /src/scripts/docker-local-replace.sh /src \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/git-service .

# --- Stage 2: runtime ----------------------------------------------------
# Alpine, non distroless: il servizio esegue il binario `git` ufficiale a
# runtime. Utente non privilegiato; /data è la directory dei repo (nel
# chart, il PVC git-data).
FROM alpine:3.22

RUN apk add --no-cache git ca-certificates \
    && adduser -D -u 10001 gitsvc \
    && mkdir -p /data \
    && chown gitsvc:gitsvc /data

COPY --from=build /out/git-service /usr/local/bin/git-service

USER gitsvc

ENV GITSTACK_GIT_DATA_DIR=/data

VOLUME ["/data"]
EXPOSE 8080 2222

ENTRYPOINT ["/usr/local/bin/git-service"]
