#!/usr/bin/env bash
# Prepara la prova reale di un agente di coding con gs e skills (GIT-174, M-07/M).
#
# Con un token amministratore crea, in modo idempotente:
#   1. l'utente agente (kind=agent, senza password);
#   2. il repo di prova, privato, con il contenuto di scripts/agent-trial/sample-repo;
#   3. il grant `write` dell'agente su quel solo repo (P4);
#   4. una issue semplice (il difetto di wordcount.py) assegnata all'agente,
#      cosi' l'agente riceve una notifica `assigned` (C3, C4);
#   5. un token dell'agente con gli scope minimi (read:resource, write:resource),
#      scadenza breve, scritto in un file 0600 e MAI stampato (P5).
#
# Uso:
#   GS_HOST=<host[:porta]> GS_ADMIN_TOKEN=<token write:user + write:resource> \
#     scripts/agent-trial/setup.sh [--rotate-token]
#
# Variabili (tutte facoltative tranne le prime due):
#   GS_HOST            istanza (come `gs`: nome o IP, con :porta se serve)
#   GS_ADMIN_TOKEN     token dell'amministratore, scope write:user e write:resource
#   GS_CA_FILE         CA interna da fidare (Linux; su macOS/Windows va nel sistema)
#   TRIAL_AGENT        nome dell'utente agente        (default trial-agent)
#   TRIAL_REPO         nome del repo di prova         (default agent-trial)
#   TRIAL_TOKEN_FILE   dove scrivere il token         (default ./agent-trial.token)
#   TRIAL_TOKEN_DAYS   giorni di validita' del token  (default 7)
#   GS                 percorso del binario gs        (default gs)
#
# --rotate-token: revoca il token `agent-trial` esistente e ne crea uno nuovo
# (il valore di un token non si puo' rileggere: senza questa opzione, se il
# token esiste gia', lo script non ne crea un altro).
#
# Funziona da Git Bash e da Linux/macOS. Ogni passo stampa "ok <passo>" e al
# primo errore esce con codice != 0 e "FAIL <passo>: <motivo>".
set -u

# Git Bash (MSYS) riscrive gli argomenti che iniziano con / (es. /auth/session) in
# percorsi Windows quando chiama un .exe: lo spegne.
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL="*"

fail() { echo "FAIL $1: $2" >&2; exit 1; }
ok() { echo "ok $1"; }
# stampa lo stdout del comando solo se esce 0 (gs api scrive il corpo dell'errore su stdout)
ok_only() { local o; if o="$("$@" 2>/dev/null)"; then printf "%s" "$o"; fi; }

rotate=0
case "${1:-}" in
  "") ;;
  --rotate-token) rotate=1 ;;
  -h | --help) sed -n '2,/^set -u/p' "$0" | sed '$d;s/^# \{0,1\}//'; exit 0 ;;
  *) echo "uso: $0 [--rotate-token]" >&2; exit 2 ;;
esac

GS="${GS:-gs}"
AGENT="${TRIAL_AGENT:-trial-agent}"
REPO_NAME="${TRIAL_REPO:-agent-trial}"
TOKEN_FILE="${TRIAL_TOKEN_FILE:-./agent-trial.token}"
DAYS="${TRIAL_TOKEN_DAYS:-7}"
TOKEN_NAME="agent-trial"
ISSUE_TITLE="wordcount conta male testo vuoto e spazi multipli"
HERE="$(cd "$(dirname "$0")" && pwd)"
SAMPLE="$HERE/sample-repo"

[ -n "${GS_HOST:-}" ] || fail env "manca GS_HOST (es. homehub.local o 192.168.1.50)"
[ -n "${GS_ADMIN_TOKEN:-}" ] || fail env "manca GS_ADMIN_TOKEN (token admin con write:user e write:resource)"
command -v "$GS" >/dev/null 2>&1 || fail env "gs non trovato (vedi docs/agent-trial.md, passo 1)"
command -v git >/dev/null 2>&1 || fail env "git non trovato"
[ -d "$SAMPLE" ] || fail env "manca $SAMPLE"
case "$DAYS" in '' | *[!0-9]*) fail env "TRIAL_TOKEN_DAYS non e' un numero: $DAYS" ;; esac

# Ambiente isolato: la configurazione di git e di gs di questa sessione non
# tocca quella dell'utente (il helper di credenziali di gs vive nel HOME temporaneo).
W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT
export HOME="$W/home"
export GS_CONFIG_DIR="$W/gs"
mkdir -p "$HOME" "$GS_CONFIG_DIR"
export GS_NO_KEYRING=1 GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME="agent-trial setup" GIT_AUTHOR_EMAIL="agent-trial@example.invalid"
export GIT_COMMITTER_NAME="agent-trial setup" GIT_COMMITTER_EMAIL="agent-trial@example.invalid"
if [ -n "${GS_CA_FILE:-}" ]; then
  [ -r "$GS_CA_FILE" ] || fail env "GS_CA_FILE non leggibile: $GS_CA_FILE"
  export SSL_CERT_FILE="$GS_CA_FILE" GIT_SSL_CAINFO="$GS_CA_FILE"
fi
export GS_TOKEN="$GS_ADMIN_TOKEN"

# --- 0. chi sono ------------------------------------------------------------
ME="$("$GS" api /auth/session --jq .user.username 2>&1)" || fail admin "gs api /auth/session: $ME"
[ -n "$ME" ] || fail admin "utente non letto"
ISADMIN="$(ok_only "$GS" api /auth/session --jq .user.isAdmin)"
[ "$ISADMIN" = "false" ] && fail admin "$ME non e' amministratore: serve un token di un admin"
REPO="$ME/$REPO_NAME"
ok "admin ($ME)"

# --- 1. utente agente ---------------------------------------------------------
AGENT_ID="$(ok_only "$GS" api "/users/$AGENT" --jq .id)"
if [ -z "$AGENT_ID" ]; then
  out="$("$GS" api -X POST /users -f "username=$AGENT" -f kind=agent -f "displayName=Agente di prova" --jq .id 2>&1)" \
    || fail agente "creazione di $AGENT: $out"
  AGENT_ID="$out"
else
  kind="$(ok_only "$GS" api "/users/$AGENT" --jq .kind)"
  [ "$kind" = agent ] || fail agente "$AGENT esiste ma e' di tipo '$kind', non agent: scegli un altro TRIAL_AGENT"
fi
ok "agente ($AGENT)"

# --- 2. repo di prova ----------------------------------------------------------
if ! "$GS" api "/repos/$REPO" --jq .id >/dev/null 2>&1; then
  out="$("$GS" repo create "$REPO_NAME" -d "Repo di prova per un agente di coding (GIT-174)" --private 2>&1)" \
    || fail repo "creazione di $REPO: $out"
fi
RID="$("$GS" api "/repos/$REPO" --jq .id 2>&1)" || fail repo "lettura di $REPO: $RID"
EMPTY="$("$GS" api "/repos/$REPO" --jq .empty 2>&1)" || fail repo "lettura di $REPO: $EMPTY"
ok "repo ($REPO)"

# --- 3. contenuto iniziale (solo se vuoto) -------------------------------------
if [ "$EMPTY" = true ]; then
  "$GS" auth setup-git >/dev/null 2>&1 || fail contenuto "gs auth setup-git"
  # il remote si ricava dall'API, cosi' vale anche con schema e porta diversi
  url="$("$GS" api "/repos/$REPO" --jq .cloneUrls.https 2>&1)" || fail contenuto "cloneUrls: $url"
  mkdir "$W/seed" && cp "$SAMPLE"/* "$W/seed/" || fail contenuto "copia di sample-repo"
  (
    cd "$W/seed" || exit 1
    git init -q -b main &&
      git add . &&
      git commit -q -m "Primo commit: wordcount con un difetto noto" &&
      git remote add origin "$url" &&
      git push -q origin main
  ) >"$W/seed.log" 2>&1 || fail contenuto "push iniziale: $(cat "$W/seed.log")"
  ok "contenuto (push su main)"
else
  ok "contenuto (repo gia' popolato, non toccato)"
fi

# --- 4. grant minimo dell'agente -----------------------------------------------
have="$("$GS" api "/resources/$RID/grants" --jq ".items[] | select(.subjectId == \"$AGENT_ID\") | .role" 2>&1)" \
  || fail grant "lettura dei grant: $have"
if [ -z "$have" ]; then
  out="$("$GS" api -X POST "/resources/$RID/grants" -f subjectType=user -f "subjectId=$AGENT_ID" -f role=write 2>&1)" \
    || fail grant "creazione: $out"
  have=write
fi
[ "$have" = write ] || echo "attenzione: $AGENT ha il ruolo '$have' su $REPO (atteso write)" >&2
ok "grant ($AGENT: $have su $REPO)"

# --- 5. issue assegnata all'agente -----------------------------------------------
num="$("$GS" issue list -R "$REPO" -s all --json number,title --jq ".[] | select(.title == \"$ISSUE_TITLE\") | .number" 2>&1)" \
  || fail issue "elenco: $num"
num="$(echo "$num" | head -n 1)"
if [ -z "$num" ]; then
  body="$(cat "$HERE/issue-body.md")" || fail issue "manca issue-body.md"
  num="$("$GS" issue create -R "$REPO" -t "$ISSUE_TITLE" -b "$body" -a "$AGENT" --json number --jq .number 2>&1)" \
    || fail issue "creazione: $num"
fi
case "$num" in '' | *[!0-9]*) fail issue "numero non valido: $num" ;; esac
ok "issue (#$num assegnata a $AGENT)"

# --- 6. token dell'agente -----------------------------------------------------
old="$("$GS" api "/users/$AGENT/tokens" --jq ".items[] | select(.name == \"$TOKEN_NAME\") | .id" 2>&1)" \
  || fail token "elenco: $old"
if [ -n "$old" ] && [ "$rotate" = 1 ]; then
  for id in $old; do
    "$GS" api -X DELETE "/users/$AGENT/tokens/$id" >/dev/null 2>&1 || fail token "revoca di $id"
  done
  old=""
fi
if [ -n "$old" ]; then
  ok "token (esiste gia' un token '$TOKEN_NAME': il valore non si rilegge, usa --rotate-token per un token nuovo)"
else
  exp="$(date -u -d "+${DAYS} days" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+"${DAYS}"d +%Y-%m-%dT%H:%M:%SZ)"
  umask 077
  tok="$("$GS" api -X POST "/users/$AGENT/tokens" -f "name=$TOKEN_NAME" \
    -F 'scopes[]=read:resource' -F 'scopes[]=write:resource' -f "expiresAt=$exp" --jq .token 2>&1)" \
    || fail token "creazione: $tok"
  case "$tok" in gst_*) ;; *) fail token "valore non valido (non stampato)" ;; esac
  printf '%s\n' "$tok" >"$TOKEN_FILE" || fail token "scrittura di $TOKEN_FILE"
  chmod 600 "$TOKEN_FILE" 2>/dev/null || true
  ok "token (scope read:resource write:resource, scade $exp, file $TOKEN_FILE)"
fi

cat <<EOF

Prova pronta.
  repo     $REPO
  issue    #$num (assegnata a $AGENT)
  agente   $AGENT   token: $TOKEN_FILE
Prossimo passo: docs/agent-trial.md, "Installa le skills e lancia l'agente".
Dopo la prova: scripts/agent-trial/check.sh
EOF
