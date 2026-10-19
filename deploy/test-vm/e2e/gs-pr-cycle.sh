#!/bin/bash
# Ciclo di una Pull Request sulla VM (GIT-273, v1.1, passo e11 di e2e.ps1).
#
# Un agente (utente kind=agent, token read+write:resource, grant write) porta una
# issue fino a una PR con `gs`; una persona (il proprietario del repo) la
# approva; l'agente la unisce e la issue si chiude come «completed». Poi lo
# stesso ciclo con `gs mcp` (stdio): l'agente apre issue e PR e unisce con gli
# strumenti MCP, la persona approva con `gs pr review`. Verifica anche che
# l'autore non approvi la propria PR (PR2) e che --read-only rifiuti le scritture (M3).
#
# Variabili (le passa e2e.ps1):
#   GS_VM_IP           indirizzo della VM (ingress HTTPS)
#   GS_CA_SHA256       impronta SHA-256 della CA interna
#   GS_USER_TOKEN      token read+write:resource della persona (proprietaria del repo)
#   GS_AGENT_TOKEN     token read+write:resource dell'utente agente
#   GS_USER_NAME       la persona
#   GS_AGENT_NAME      l'utente agente
#   GS_AGENT_ID        il suo id (il token della persona non ha read:user)
#   GS_REPO_NAME       nome del repo da creare
# Ogni passo stampa "STEP ok <nome>"; al primo errore esce con codice != 0
# dopo aver stampato "STEP FAIL <nome>: <motivo>".
set -u

W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT
export HOME="$W/home"
mkdir -p "$HOME" "$W/bin"
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=E2E GIT_AUTHOR_EMAIL=e2e@example.com GIT_COMMITTER_NAME=E2E GIT_COMMITTER_EMAIL=e2e@example.com
BASE="https://${GS_VM_IP}"

fail() { echo "STEP FAIL $1: $2"; exit 1; }
ok() { echo "STEP ok $1"; }

# --- installazione di gs da /downloads (impronta della CA verificata) -------
curl -fsSLk -o "$W/install-gs.sh" "$BASE/install-gs.sh" || fail install "download di install-gs.sh"
out="$(GS_BASE_URL="$BASE" GS_INSTALL_DIR="$W/bin" GS_CA_SHA256="$GS_CA_SHA256" sh "$W/install-gs.sh" 2>&1)" || fail install "install-gs.sh: $out"
[ -x "$W/bin/gs" ] || fail install "gs non installato"
CA="$(ls "$HOME"/.config/gitstack/ca-*.crt 2>/dev/null | head -n 1)"
[ -n "$CA" ] || fail install "CA non salvata dall'installer"
ok install

export PATH="$W/bin:$PATH"
export SSL_CERT_FILE="$CA" GIT_SSL_CAINFO="$CA"
export GS_HOST="${GS_VM_IP}"
export GS_NO_KEYRING=1
GS="$W/bin/gs"
REPO="${GS_USER_NAME}/${GS_REPO_NAME}"

as_person() { GS_TOKEN="$GS_USER_TOKEN" "$GS" "$@"; }
as_agent() { GS_TOKEN="$GS_AGENT_TOKEN" "$GS" "$@"; }
# git con le credenziali di gs del chiamante (helper di `gs auth setup-git`)
GS_TOKEN="$GS_USER_TOKEN" "$GS" auth setup-git >/dev/null 2>&1 || fail setup_git "gs auth setup-git: exit $?"

out="$(as_person repo create "$GS_REPO_NAME" --json fullName 2>&1)" || fail repo_create "$out"
case "$out" in *"$REPO"*) ;; *) fail repo_create "atteso $REPO in: $out" ;; esac
ok repo_create

# La persona dà write all'agente: il grant sul repo (id dell'agente da gs api).
rid="$(as_person repo view "$REPO" --json id --jq .id 2>&1)" || fail grant "repo view: $rid"
aid="${GS_AGENT_ID:?id utente agente}"
as_person api -X POST "/resources/${rid}/grants" -f subjectType=user -f "subjectId=${aid}" -f role=write >/dev/null 2>&1 || fail grant "grant write all'agente"
ok grant

# Il ramo principale deve esistere: lo spinge la persona.
cd "$W" || exit 1
as_person repo clone "$REPO" "$W/person" >/dev/null 2>&1 || fail clone "clone della persona"
cd "$W/person" || exit 1
echo "# ciclo PR" > README.md
git add README.md
git commit -q -m "Primo commit" || fail clone "commit del ramo principale"
GS_TOKEN="$GS_USER_TOKEN" git push -q origin HEAD:main 2>&1 || fail clone "push del ramo principale"
ok clone

# La credenziale per git dell'agente: URL con il suo token (non finisce nei log).
AURL="https://${GS_AGENT_NAME}:${GS_AGENT_TOKEN}@${GS_VM_IP}/${REPO}.git"

# agent_cycle <etichetta> <metodo> usa gs; la parte MCP è sotto.
poll_issue_closed() {
  local num="$1" state=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    state="$(as_person issue view "$num" -R "$REPO" --json state,closeReason --jq '.state+"/"+.closeReason' 2>/dev/null)"
    [ "$state" = "closed/completed" ] && return 0
    sleep 3
  done
  echo "$state"
  return 1
}

branch_with_file() { # <ramo> <file> <numero issue>
  rm -rf "$W/agent" && git clone -q "$AURL" "$W/agent" 2>&1 || return 1
  ( cd "$W/agent" || exit 1
    git config user.name "$GS_AGENT_NAME" && git config user.email "${GS_AGENT_NAME}@agents.example.com"
    git switch -q -c "$1" && echo "contenuto di $2" > "$2" && git add "$2"
    git commit -q -m "Aggiunge $2

fixes #$3" && git push -q origin "$1" 2>&1 )
}

# =========================== via gs ======================================
num="$(as_agent issue create -R "$REPO" -t "Manca la guida" -b "da scrivere" --json number --jq .number 2>&1)" || fail issue_create "$num"
case "$num" in '' | *[!0-9]*) fail issue_create "numero non valido: $num" ;; esac
ok issue_create

branch_with_file "agent/${num}-guida" GUIDA.md "$num" || fail branch_push "commit e push del ramo dell'agente"
ok branch_push

pn="$(as_agent pr create -R "$REPO" --head "agent/${num}-guida" --title "Aggiunge la guida" --body "fixes #${num}" --draft --json number --jq .number 2>&1)" || fail pr_create "$pn"
case "$pn" in '' | *[!0-9]*) fail pr_create "numero non valido: $pn" ;; esac
[ "$(as_agent pr view "$pn" -R "$REPO" --json draft --jq .draft)" = true ] || fail pr_create "la PR non è una bozza"
ok pr_create

as_agent pr merge "$pn" -R "$REPO" --squash --yes >/dev/null 2>&1 && fail pr_bozza "una bozza si è unita"
ok pr_bozza

as_agent pr ready "$pn" -R "$REPO" --reviewer "$GS_USER_NAME" >/dev/null 2>&1 || fail pr_ready "gs pr ready"
ok pr_ready

as_agent pr review "$pn" -R "$REPO" --approve >/dev/null 2>&1 && fail review_propria "l'agente ha approvato la propria PR"
ok review_propria

out="$(as_person pr review "$pn" -R "$REPO" --approve --body "Ok" --json state,countsAsApproval 2>&1)" || fail pr_review "$out"
case "$out" in *\"state\":\"approved\"*\"countsAsApproval\":true*|*\"countsAsApproval\":true*\"state\":\"approved\"*) ;; *) fail pr_review "approvazione della persona non valida: $out" ;; esac
dec="$(as_agent pr view "$pn" -R "$REPO" --json reviewDecision --jq .reviewDecision 2>&1)"
[ "$dec" = approved ] || fail pr_review "reviewDecision = '$dec'"
ok pr_review

as_agent pr comment "$pn" -R "$REPO" --body "Grazie, unisco." >/dev/null 2>&1 || fail pr_comment "gs pr comment"
ok pr_comment

out="$(as_agent pr merge "$pn" -R "$REPO" --squash --delete-branch --yes --json state,mergeMethod 2>&1)" || fail pr_merge "$out"
case "$out" in *\"mergeMethod\":\"squash\"*\"state\":\"merged\"*|*\"state\":\"merged\"*\"mergeMethod\":\"squash\"*) ;; *) fail pr_merge "atteso merged/squash in: $out" ;; esac
ok pr_merge

left="$(poll_issue_closed "$num")" || fail issue_chiusa "stato '$left' dopo il merge della PR con fixes #$num"
ok issue_chiusa

# =========================== via MCP =====================================
# Una chiamata = un processo `gs mcp` (stdio) con i tre messaggi del protocollo;
# il risultato è il testo del primo contenuto della risposta con id 2.
mcp_call() { # <token> <flag-extra|-> <strumento> <argomenti JSON>
  local tok="$1" extra="$2" tool="$3" args="$4" flags=()
  [ "$extra" != "-" ] && flags=("$extra")
  printf '%s\n%s\n%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"${tool}\",\"arguments\":${args}}}" |
    GS_TOKEN="$tok" "$GS" mcp "${flags[@]}" 2>/dev/null |
    python3 -c '
import json, sys
for line in sys.stdin:
    r = json.loads(line)
    if r.get("id") == 2:
        c = r["result"]
        print(("ERR " if c.get("isError") else "OK ") + c["content"][0]["text"])
        sys.exit(0)
print("ERR nessuna risposta")
'
}
mcp_field() { python3 -c 'import json,sys; print(json.loads(sys.stdin.read()[3:])[sys.argv[1]])' "$1"; }

tools="$(printf '%s\n%s\n%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | GS_TOKEN="$GS_AGENT_TOKEN" "$GS" mcp 2>/dev/null)"
for t in issue_create pr_create pr_ready pr_merge api_request; do
  case "$tools" in *"\"$t\""*) ;; *) fail mcp_strumenti "strumento $t assente" ;; esac
done
ok mcp_strumenti

r="$(mcp_call "$GS_AGENT_TOKEN" - issue_create "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"body\":{\"title\":\"Manca la guida MCP\",\"body\":\"da scrivere\"}}")"
case "$r" in OK*) ;; *) fail mcp_issue "$r" ;; esac
mnum="$(printf '%s' "$r" | mcp_field number)"
branch_with_file "agent/${mnum}-guida-mcp" GUIDA-MCP.md "$mnum" || fail mcp_branch "commit e push del ramo dell'agente"
r="$(mcp_call "$GS_AGENT_TOKEN" - pr_create "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"body\":{\"title\":\"Aggiunge la guida MCP\",\"sourceBranch\":\"agent/${mnum}-guida-mcp\",\"body\":\"fixes #${mnum}\",\"draft\":true}}")"
case "$r" in OK*) ;; *) fail mcp_pr_create "$r" ;; esac
mpn="$(printf '%s' "$r" | mcp_field number)"
r="$(mcp_call "$GS_AGENT_TOKEN" - pr_merge "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"number\":${mpn},\"body\":{\"method\":\"squash\"}}")"
case "$r" in ERR*) ;; *) fail mcp_bozza "una bozza si è unita via MCP: $r" ;; esac
r="$(mcp_call "$GS_AGENT_TOKEN" - pr_ready "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"number\":${mpn}}")"
case "$r" in OK*) ;; *) fail mcp_pr_ready "$r" ;; esac
ok mcp_pr_create

# la persona approva con gs (PR2); l'agente unisce con lo strumento MCP
out="$(as_person pr review "$mpn" -R "$REPO" --approve --json state,countsAsApproval 2>&1)" || fail mcp_review "$out"
case "$out" in *\"state\":\"approved\"*\"countsAsApproval\":true*|*\"countsAsApproval\":true*\"state\":\"approved\"*) ;; *) fail mcp_review "approvazione della persona non valida: $out" ;; esac
ok mcp_review

r="$(mcp_call "$GS_AGENT_TOKEN" - pr_merge "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"number\":${mpn},\"body\":{\"method\":\"merge\"}}")"
case "$r" in OK*merged*) ;; *) fail mcp_pr_merge "$r" ;; esac
ok mcp_pr_merge

left="$(poll_issue_closed "$mnum")" || fail mcp_issue_chiusa "stato '$left' dopo il merge via MCP con fixes #$mnum"
ok mcp_issue_chiusa

# M3: --read-only rifiuta le scritture, anche con api_request, e lascia leggere
r="$(mcp_call "$GS_AGENT_TOKEN" --read-only issue_create "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"body\":{\"title\":\"no\"}}")"
case "$r" in ERR*) ;; *) fail mcp_read_only "issue_create riuscito in sola lettura: $r" ;; esac
r="$(mcp_call "$GS_AGENT_TOKEN" --read-only api_request "{\"method\":\"POST\",\"path\":\"/repos/${REPO}/issues\",\"body\":{\"title\":\"no\"}}")"
case "$r" in ERR*) ;; *) fail mcp_read_only "api_request POST riuscito in sola lettura: $r" ;; esac
r="$(mcp_call "$GS_AGENT_TOKEN" --read-only pr_view "{\"owner\":\"${GS_USER_NAME}\",\"repo\":\"${GS_REPO_NAME}\",\"number\":${mpn}}")"
case "$r" in OK*merged*) ;; *) fail mcp_read_only "pr_view in sola lettura: $r" ;; esac
ok mcp_read_only

echo "GS-PR-CYCLE-DONE"
