# syntax=docker/dockerfile:1
#
# Immagine del servizio gateway. Contesto di build: la RADICE del monorepo
# (GIT-184); i moduli interni (pkg/*) si compilano dalle sorgenti locali.
#
#   docker build -f services/gateway/Dockerfile .
#
# Il tag e il push nel registry li fa il job `registry` di ci.yml.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

# Contesto di build: la RADICE del monorepo (GIT-184). I moduli interni
# (pkg/*, client/go) si compilano dalle sorgenti locali del commit, non dalla
# pseudo-versione del go.mod: scripts/docker-local-replace.sh scrive i replace.
#   docker build -f services/gateway/Dockerfile .
WORKDIR /src
COPY pkg ./pkg
COPY scripts/docker-local-replace.sh ./scripts/docker-local-replace.sh

WORKDIR /src/services/gateway

# Livello di cache separato per i moduli di terze parti.
COPY services/gateway/go.mod services/gateway/go.sum ./
RUN sh /src/scripts/docker-local-replace.sh /src \
    && for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY services/gateway/ .

RUN sh /src/scripts/docker-local-replace.sh /src \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway .

# --- Stage 2: runtime ----------------------------------------------------
# distroless "static", non "base": il binario è staticamente linkato
# (CGO_ENABLED=0) e non ha bisogno di libc. Nessuna shell, nessun package
# manager: superficie d'attacco minima. Utente non privilegiato (nonroot).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/gateway /usr/local/bin/gateway

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/gateway"]
