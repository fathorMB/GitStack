# syntax=docker/dockerfile:1
#
# Immagine del gateway GitStack. Contesto di build: questa cartella
# (services/gateway), non la radice del monorepo: il modulo Go del gateway
# non dipende da altri moduli del workspace (parla con core solo via HTTP,
# GITSTACK_CORE_URL), quindi il modulo si compila da solo.
#
#   docker build -f services/gateway/Dockerfile services/gateway
#
# Il tag e il push nel registry interno li fa il job `registry` (GIT-2): non
# è compito di questa immagine.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Livello di cache separato per i moduli: cambia solo quando cambiano
# go.mod/go.sum, non a ogni modifica del codice.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway .

# --- Stage 2: runtime ----------------------------------------------------
# distroless "static", non "base": il binario è staticamente linkato
# (CGO_ENABLED=0) e non ha bisogno di libc. Nessuna shell, nessun package
# manager: superficie d'attacco minima. Utente non privilegiato (nonroot).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/gateway /usr/local/bin/gateway

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/gateway"]
