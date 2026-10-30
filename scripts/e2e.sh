#!/usr/bin/env bash
# Teste ponta a ponta do modulo de exemplo contra um Core de verdade, sem privilegios: o daemon
# sobe com o modulo declarado como externo, o modulo conecta pelo socket (autenticado pelo UID),
# envia eventos genericos, a regra generic.burst dispara e o modulo aparece na CLI e na TUI.
#
# Onde esta o Core (nesta ordem):
#   PSYCHOSCOPE_BIN_DIR   pasta com os binarios psychoscope e psychoscope-cli (por exemplo, de uma release)
#   PSYCHOSCOPE_CORE      checkout do psychoscope-core (compilado aqui; o padrao e ../psychoscope-core)
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
if ! command -v go >/dev/null && [ -d "$HOME/.local/go/bin" ]; then export PATH="$HOME/.local/go/bin:$PATH"; fi
TMP="$(mktemp -d)"
DPID=""; MPID=""
cleanup() {
  [ -z "$MPID" ] || kill "$MPID" 2>/dev/null || true
  [ -z "$DPID" ] || kill "$DPID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT
fail() { echo "FALHOU: $*" >&2; echo "--- log do daemon ---" >&2; cat "$TMP/daemon.log" >&2 || true; echo "--- log do modulo ---" >&2; cat "$TMP/module.log" >&2 || true; exit 1; }

CORE_BIN=""
if [ -n "${PSYCHOSCOPE_BIN_DIR:-}" ]; then
  CORE_BIN="$PSYCHOSCOPE_BIN_DIR"
else
  CORE="${PSYCHOSCOPE_CORE:-$HERE/../psychoscope-core}"
  [ -f "$CORE/go.mod" ] || fail "psychoscope-core nao encontrado em $CORE (defina PSYCHOSCOPE_CORE ou PSYCHOSCOPE_BIN_DIR)"
  CORE_BIN="$TMP/core"; mkdir -p "$CORE_BIN"
  (cd "$CORE" && go build -o "$CORE_BIN/psychoscope" ./cmd/psychoscope && go build -o "$CORE_BIN/psychoscope-cli" ./cmd/psychoscope-cli)
fi
(cd "$HERE" && go build -o "$TMP/mod-example" ./cmd/mod-example)
PTY="${PSYCHOSCOPE_CORE:-$HERE/../psychoscope-core}/scripts/tui_pty.py"

ME="$(id -un)"
mkdir -p "$TMP/run" "$TMP/state"
cat > "$TMP/cfg.json" <<JSON
{
  "status_interval": "1s",
  "modules": [{ "id": "mod-example", "external": true, "user": "$ME" }],
  "detection": { "generic_burst": { "types": ["login.falha"], "group_by": ["usuario"], "min_events": 10, "window": "60s" } },
  "storage": { "path": "$TMP/state/alerts.db" },
  "overrides_path": "$TMP/state/overrides.json", "engine_state_path": "$TMP/state/engine-state.json"
}
JSON
CLI="$CORE_BIN/psychoscope-cli --ctl-socket $TMP/run/ctl.sock"

start_daemon() {
  "$CORE_BIN/psychoscope" --config "$TMP/cfg.json" --socket "$TMP/run/core.sock" --ctl-socket "$TMP/run/ctl.sock" 2>> "$TMP/daemon.log" &
  DPID=$!
}
wait_for() { # descricao, comando...
  local what="$1"; shift
  for _ in $(seq 150); do if "$@" >/dev/null 2>&1; then return 0; fi; sleep 0.2; done
  fail "timeout esperando: $what"
}
# shellcheck disable=SC2086 # CLI guarda o binario e suas flags
col() { $CLI list | awk -v c="$1" 'NR==1{for(i=1;i<=NF;i++) if($i==c) idx=i} NR==2{print $idx}'; }
state_is() { [ "$(col ESTADO)" = "$1" ]; }

start_daemon
wait_for "ctl.sock" test -S "$TMP/run/ctl.sock"
# O modulo e externo: quem o inicia e o systemd em producao; aqui, este script.
"$TMP/mod-example" --socket "$TMP/run/core.sock" --id mod-example --rate 5 --burst-every 1s --burst-size 12 --burst-user ana 2> "$TMP/module.log" &
MPID=$!
wait_for "modulo CONECTADO" state_is CONECTADO
echo "ok: modulo conectado (handshake e UID do kernel)"

wait_for "EVENTOS > 0" bash -c "[ \"\$($(declare -f col); CLI='$CLI'; col EVENTOS)\" -gt 0 ]"
[ "$(col PACOTES)" = "-" ] || fail "um modulo sem pacotes deve mostrar '-' em PACOTES, veio '$(col PACOTES)'"
echo "ok: eventos recebidos; PACOTES mostra '-' para um modulo que nao captura pacotes"

# A regra generic.burst dispara uma vez (cooldown de 5 min) para o usuario da rajada.
wait_for "alerta generic.burst" bash -c "$CLI alerts --history | grep -q generic.burst"
# shellcheck disable=SC2086
$CLI alerts --history -v > "$TMP/alerts.txt"
[ "$(grep -c 'generic.burst' "$TMP/alerts.txt")" = 1 ] || fail "esperado exatamente um alerta generic.burst: $(cat "$TMP/alerts.txt")"
grep -q "usuario=ana" "$TMP/alerts.txt" || fail "o alerta deveria citar o grupo usuario=ana: $(cat "$TMP/alerts.txt")"
grep -q "mod-example" "$TMP/alerts.txt" || fail "o alerta deveria citar o modulo de origem"
echo "ok: alerta generic.burst (grupo usuario=ana), um so, apesar das rajadas repetidas"

# Comandos do Core: PAUSAR e RETOMAR sao confirmados; RECONFIGURAR recebe NACK com o motivo.
$CLI cmd mod-example pause > /dev/null || fail "pause"
wait_for "modulo PAUSADO" state_is PAUSADO
$CLI cmd mod-example resume > /dev/null || fail "resume"
wait_for "modulo CONECTADO de novo" state_is CONECTADO
if $CLI cmd mod-example reconfigure 2> "$TMP/nack.txt"; then fail "reconfigure deveria receber NACK"; fi
grep -q "reconfiguracao" "$TMP/nack.txt" || fail "o NACK deveria explicar o motivo: $(cat "$TMP/nack.txt")"
echo "ok: PAUSAR, RETOMAR e o NACK de RECONFIGURAR"

# A TUI num pseudo-terminal real mostra o modulo e o alerta.
if [ -f "$PTY" ] && command -v python3 >/dev/null; then
  python3 "$PTY" "$CORE_BIN/psychoscope-cli" "$TMP/run/ctl.sock" PSYCHOSCOPE mod-example CONECTADO "Rajada de eventos genéricos" || fail "TUI"
  echo "ok: TUI mostra o modulo e o alerta"
else
  echo "aviso: tui_pty.py ou python3 ausente; TUI nao verificada"
fi

# O Core reinicia: o modulo reconecta sozinho (backoff) e volta a ser aceito.
kill "$DPID"; wait "$DPID" 2>/dev/null || true; DPID=""
rm -f "$TMP/run/core.sock" "$TMP/run/ctl.sock"
start_daemon
wait_for "ctl.sock apos reinicio" test -S "$TMP/run/ctl.sock"
wait_for "modulo reconectado" state_is CONECTADO
echo "ok: o modulo reconectou depois do reinicio do Core"

# ENCERRAR: o modulo confirma e sai com codigo 0 (o systemd nao o reinicia com Restart=on-failure).
$CLI cmd mod-example stop > /dev/null || fail "stop"
for _ in $(seq 50); do kill -0 "$MPID" 2>/dev/null || break; sleep 0.1; done
if kill -0 "$MPID" 2>/dev/null; then fail "o modulo deveria sair depois do ENCERRAR"; fi
if wait "$MPID"; then echo "ok: ENCERRAR confirmado e o modulo saiu com codigo 0"; else fail "o modulo saiu com erro depois do ENCERRAR"; fi
MPID=""
echo "E2E OK"
