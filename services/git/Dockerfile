# syntax=docker/dockerfile:1
#
# Immagine del servizio git. Contesto di build: questa cartella (services/git),
# non la radice del monorepo: il modulo Go non dipende da altri moduli del
# workspace (golang.org/x/crypto per il server SSH sta in go.mod/go.sum),
# quindi si compila da solo.
#
#   docker build -f services/git/Dockerfile services/git
#
# Il tag e il push nel registry li fa il job `registry` di ci.yml.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/git-service .

# --- Stage 2: runtime ----------------------------------------------------
# Alpine, non distroless: il servizio esegue il binario `git` ufficiale a
# runtime. Utente non privilegiato; /data è la directory dei repo (nel
# chart, il PVC git-data).
FROM alpine:3.22

RUN apk add --no-cache git \
    && adduser -D -u 10001 gitsvc \
    && mkdir -p /data \
    && chown gitsvc:gitsvc /data

COPY --from=build /out/git-service /usr/local/bin/git-service

USER gitsvc

ENV GITSTACK_GIT_DATA_DIR=/data

VOLUME ["/data"]
EXPOSE 8080 2222

ENTRYPOINT ["/usr/local/bin/git-service"]
