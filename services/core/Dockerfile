# syntax=docker/dockerfile:1
#
# Immagine di core. Contesto di build: questa cartella (services/core), non
# la radice del monorepo: il modulo Go di core non dipende da altri moduli
# del workspace (parla con Postgres via pgx, in futuro con NATS via la
# libreria di GIT-6), quindi il modulo si compila da solo.
#
#   docker build -f services/core/Dockerfile services/core
#
# Il tag e il push nel registry interno li fa il job `registry` (GIT-2): non
# è compito di questa immagine.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Livello di cache separato per i moduli: cambia solo quando cambiano
# go.mod/go.sum, non a ogni modifica del codice. go.sum include anche le
# dipendenze usate solo dai test d'integrazione (testcontainers-go, build
# tag "integration"): non finiscono nel binario (il codice che le importa
# non è raggiungibile da main), ma "go mod download" le scarica comunque.
COPY go.mod go.sum ./
RUN for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/core .

# --- Stage 2: runtime ----------------------------------------------------
# distroless "static", non "base": il binario è staticamente linkato
# (CGO_ENABLED=0) e non ha bisogno di libc. Nessuna shell, nessun package
# manager: superficie d'attacco minima. Utente non privilegiato (nonroot).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/core /usr/local/bin/core

USER nonroot:nonroot

EXPOSE 8080

# Applica le migrazioni e avvia il server (vedi main.go: `core` senza
# argomenti equivale a `core serve`, con le migrazioni applicate prima
# dell'avvio). Per un job dedicato che applichi solo le migrazioni (utile
# come init container/Job Helm, GIT-8), sovrascrivere il comando con
# ["core", "migrate", "up"].
ENTRYPOINT ["/usr/local/bin/core"]
