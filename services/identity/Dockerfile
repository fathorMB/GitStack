# syntax=docker/dockerfile:1
#
# Immagine del servizio identity. Contesto di build: la RADICE del monorepo
# (GIT-184); i moduli interni (pkg/*) si compilano dalle sorgenti locali.
#
#   docker build -f services/identity/Dockerfile .
#
# Il tag e il push nel registry li fa il job `registry` di ci.yml.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

# Contesto di build: la RADICE del monorepo (GIT-184). I moduli interni
# (pkg/*, client/go) si compilano dalle sorgenti locali del commit, non dalla
# pseudo-versione del go.mod: scripts/docker-local-replace.sh scrive i replace.
#   docker build -f services/identity/Dockerfile .
WORKDIR /src
COPY pkg ./pkg
COPY scripts/docker-local-replace.sh ./scripts/docker-local-replace.sh

WORKDIR /src/services/identity

# Livello di cache separato per i moduli di terze parti.
COPY services/identity/go.mod services/identity/go.sum ./
RUN sh /src/scripts/docker-local-replace.sh /src \
    && for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY services/identity/ .

RUN sh /src/scripts/docker-local-replace.sh /src \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/identity .

# --- Stage 2: runtime ----------------------------------------------------
# distroless "static": binario staticamente linkato, nessuna shell, utente
# non privilegiato. Le migrazioni SQL sono incorporate nel binario (embed).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/identity /usr/local/bin/identity

USER nonroot:nonroot

EXPOSE 8080

# `identity` senza argomenti equivale a `identity serve`: applica le
# migrazioni e avvia il server. Per un job dedicato: ["identity", "migrate", "up"].
ENTRYPOINT ["/usr/local/bin/identity"]
