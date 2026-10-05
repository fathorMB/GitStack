# syntax=docker/dockerfile:1
#
# Immagine di identity. Contesto di build: questa cartella (services/identity),
# non la radice del monorepo: il modulo Go di identity non dipende da altri
# moduli del workspace (parla con Postgres via pgx), quindi si compila da solo.
#
#   docker build -f services/identity/Dockerfile services/identity
#
# Il tag e il push nel registry li fa il job `registry` di ci.yml: non è
# compito di questa immagine.

# --- Stage 1: build -----------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Livello di cache separato per i moduli.
COPY go.mod go.sum ./
RUN for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/identity .

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
