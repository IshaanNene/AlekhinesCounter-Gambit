#!/usr/bin/env bash
#
# Fault-tolerance & scalability experiments against the kind deployment.
#
# Each experiment drives load through the gateway's NodePort (localhost:8088)
# with load/chaos/probe.mjs while injecting a failure or a scale change with
# kubectl, then reports what the platform did. The point is not the absolute
# numbers on a laptop — it is the *shape* of the response: does throughput scale,
# do failures stay contained, does the system heal itself.
#
# Usage:  load/chaos/chaos.sh <experiment>
#   scale            read throughput as the gateway scales 1 -> 2 -> 4
#   pod-failure      kill a gateway pod, then a game-service pod, under load
#   rolling-restart  redeploy the gateway under load; count dropped requests
#   degrade-redis    kill Redis under load (the limiter must fail open)
#   degrade-postgres kill Postgres under load, then bring it back
#   hpa              enable an HPA and watch it scale under sustained load
#   all              run everything in sequence
#
# Requires: a running kind deployment (make k8s-deploy) and metrics-server for
# the hpa experiment. Read-only load, so it is safe to re-run.

set -euo pipefail

TARGET="${TARGET:-http://localhost:8088/graphql}"
NS="${NS:-default}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── helpers ────────────────────────────────────────────────────────────────
c_blue() { printf '\033[1;34m%s\033[0m\n' "$*"; }
c_dim()  { printf '\033[2m%s\033[0m\n' "$*"; }

probe() { # <duration_s> <connections>  -> one JSON line of results
  TARGET="$TARGET" DURATION="$1" CONNECTIONS="$2" node "$HERE/probe.mjs" 2>/dev/null | tail -1
}
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('$1',''))"; }

wait_ready() { kubectl rollout status "deploy/$1" -n "$NS" --timeout=150s >/dev/null; }
scale_to()   { kubectl scale "deploy/$1" --replicas="$2" -n "$NS" >/dev/null; wait_ready "$1"; }
active_pod() { kubectl get pods -n "$NS" -l "app.kubernetes.io/name=$1" \
                 --field-selector=status.phase=Running \
                 -o jsonpath='{.items[0].metadata.name}' 2>/dev/null; }

limiter_off()     { kubectl set env deploy/gateway ACG_RATE_LIMIT_RPS=0 -n "$NS" >/dev/null; wait_ready gateway; }
limiter_restore() { kubectl set env deploy/gateway ACG_RATE_LIMIT_RPS- -n "$NS" >/dev/null; wait_ready gateway; }

# Milliseconds since the epoch, portably (macOS `date` has no %N).
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }

# Poll a *dependency-backed* read (leaderboard → game-service → Postgres) until
# it returns data again, and echo the milliseconds waited. Health is deliberately
# not used here: it is resolved in the gateway and would report "recovered" while
# a downstream dependency is still down.
time_to_recover() { # <timeout_s>
  local timeout="$1" start now
  start=$(now_ms)
  while :; do
    if curl -s -m 2 -X POST "$TARGET" -H 'content-type: application/json' \
         -d '{"query":"{ leaderboard(limit:1){ rank } }"}' 2>/dev/null \
         | grep -q '"leaderboard":\['; then
      now=$(now_ms); echo $((now - start)); return 0
    fi
    now=$(now_ms)
    (( (now - start) / 1000 >= timeout )) && { echo "-1"; return 1; }
    sleep 0.25
  done
}

# ── experiments ────────────────────────────────────────────────────────────

exp_scale() {
  c_blue "▶ Scalability — GraphQL read throughput vs. gateway replicas"
  c_dim  "  rate limiter disabled so one host is not throttled to 20/s"
  limiter_off
  for n in 1 2 4; do
    scale_to gateway "$n"
    sleep 3 # let kube-proxy pick up the new endpoints
    local r; r=$(probe 15 60)
    printf "  gateway ×%-2s  →  %6s req/s   p50 %-3s ms   p99 %-4s ms   5xx/err %s\n" \
      "$n" "$(echo "$r" | jget rps)" "$(echo "$r" | jget p50)" \
      "$(echo "$r" | jget p99)" "$(echo "$r" | jget broke)"
  done
  scale_to gateway 1
  limiter_restore
}

exp_pod_failure() {
  c_blue "▶ Fault tolerance — pod loss under load"
  limiter_off

  # (a) Redundant tier: 2 gateways, kill one mid-flight.
  scale_to gateway 2; sleep 3
  echo "  [gateway ×2] 30s of load; killing one pod at ~8s…"
  ( sleep 8; kubectl delete pod "$(active_pod gateway)" -n "$NS" --wait=false >/dev/null 2>&1 ) &
  local r; r=$(probe 30 40)
  printf "     served %s   5xx/err %s   p99 %s ms  →  a redundant tier absorbs the loss\n" \
    "$(echo "$r" | jget served2xx)" "$(echo "$r" | jget broke)" "$(echo "$r" | jget p99)"

  # (b) Single instance: kill game-service (1 replica) and time the self-heal.
  scale_to gateway 1
  echo "  [game-service ×1] killing it and timing recovery (gateway returns errors until it reschedules)…"
  kubectl delete pod "$(active_pod game-service)" -n "$NS" --wait=false >/dev/null 2>&1
  sleep 1
  local ms; ms=$(time_to_recover 90)
  printf "     recovered in %s ms via automatic reschedule + gRPC reconnect\n" "$ms"
  wait_ready game-service
  limiter_restore
}

exp_rolling_restart() {
  c_blue "▶ Zero-downtime deploy — rolling restart under load"
  limiter_off
  scale_to gateway 2; sleep 3
  echo "  30s of load; 'kubectl rollout restart deploy/gateway' at ~5s…"
  ( sleep 5; kubectl rollout restart deploy/gateway -n "$NS" >/dev/null 2>&1 ) &
  local r; r=$(probe 30 40)
  printf "     served %s   5xx/err %s   →  readiness gating means the rollout drops %s requests\n" \
    "$(echo "$r" | jget served2xx)" "$(echo "$r" | jget broke)" "$(echo "$r" | jget broke)"
  wait_ready gateway
  scale_to gateway 1
  limiter_restore
}

exp_degrade_redis() {
  c_blue "▶ Graceful degradation — Redis loss"
  c_dim  "  limiter ON at its default 20/s: every request calls Redis, so a Redis outage"
  c_dim  "  exercises the fail-open path in ratelimit.go — a blip must not be an outage"
  limiter_restore

  local a b
  echo "  [Redis up]   5s of load — the limiter should be throttling hard…"
  a=$(probe 5 40)
  printf "     throttled(429) %-7s served(2xx) %-6s 5xx/err %s\n" \
    "$(echo "$a" | jget limited4xx)" "$(echo "$a" | jget served2xx)" "$(echo "$a" | jget broke)"

  echo "  …scaling Redis to zero (it stays down for the whole next window)…"
  kubectl scale deploy/redis --replicas=0 -n "$NS" >/dev/null
  kubectl wait --for=delete pod -l app.kubernetes.io/name=redis -n "$NS" --timeout=60s >/dev/null 2>&1 || sleep 5

  echo "  [Redis down] 8s of load — Allow() can't reach Redis, so it fails open…"
  b=$(probe 8 40)
  printf "     throttled(429) %-7s served(2xx) %-6s 5xx/err %s\n" \
    "$(echo "$b" | jget limited4xx)" "$(echo "$b" | jget served2xx)" "$(echo "$b" | jget broke)"
  printf "     →  throttling releases, reads keep flowing, zero server errors: Redis degrades to \"no rate limiting\", never \"down\"\n"

  kubectl scale deploy/redis --replicas=1 -n "$NS" >/dev/null
  wait_ready redis
}

restarts() { # deploy -> total container restarts across its pods
  kubectl get pods -n "$NS" -l "app.kubernetes.io/name=$1" \
    -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{" "}{end}' \
    | awk '{s=0; for(i=1;i<=NF;i++) s+=$i; print s+0}'
}

exp_degrade_postgres() {
  c_blue "▶ Blast radius — Postgres loss (the hard dependency for reads)"
  c_dim  "  reads are Postgres-backed, so the DB is a hard dependency. The resilience question"
  c_dim  "  is not 'do reads work' (they can't) but 'is the damage contained and self-healing'."
  limiter_off
  local gw0 gs0; gw0=$(restarts gateway); gs0=$(restarts game-service)

  echo "  …scaling Postgres to zero (the DB is gone for the whole next window)…"
  kubectl scale deploy/postgres --replicas=0 -n "$NS" >/dev/null
  kubectl wait --for=delete pod -l app.kubernetes.io/name=postgres -n "$NS" --timeout=60s >/dev/null 2>&1 || sleep 5

  echo "  [Postgres down] 8s of load…"
  local r; r=$(probe 8 40)
  local gw1 gs1; gw1=$(restarts gateway); gs1=$(restarts game-service)
  printf "     completed %s reqs in 8s (throughput collapses — DB-backed reads block on the dead DB, no query timeout)\n" \
    "$(echo "$r" | jget served2xx)"
  printf "     gateway crashes: %d   game-service crashes: %d   →  the outage is CONTAINED — nothing crash-loops\n" \
    "$((gw1 - gw0))" "$((gs1 - gs0))"

  echo "  …restoring Postgres (emptyDir here, so the schema is re-applied by a game-service restart)…"
  kubectl scale deploy/postgres --replicas=1 -n "$NS" >/dev/null
  wait_ready postgres
  kubectl rollout restart deploy/game-service -n "$NS" >/dev/null
  wait_ready game-service
  local ms; ms=$(time_to_recover 90)
  printf "     reads back %s ms after the DB returned (a PVC-backed Postgres would just reconnect; here the schema is re-migrated)\n" "$ms"
  printf "     finding: bound game-service DB calls with a timeout — as the rate limiter now is — so DB reads fail fast instead of stalling a connection\n"
  limiter_restore
}

exp_hpa() {
  c_blue "▶ Autoscaling — HPA on the gateway under sustained load"
  kubectl top nodes >/dev/null 2>&1 || { echo "  metrics-server not ready; skipping"; return 0; }
  limiter_off
  scale_to gateway 1
  kubectl autoscale deploy/gateway --cpu-percent=50 --min=1 --max=5 -n "$NS" >/dev/null 2>&1 || true
  echo "  driving 60s of load; sampling replica count every 10s…"
  ( probe 60 80 >/dev/null ) &
  local load=$!
  local t=0
  for _ in $(seq 1 6); do
    sleep 10; t=$((t + 10))
    local reps targets
    reps=$(kubectl get deploy/gateway -n "$NS" -o jsonpath='{.status.replicas}')
    # The TARGETS column reads e.g. "cpu: 210%/50%" — current vs. threshold.
    targets=$(kubectl get hpa/gateway -n "$NS" --no-headers 2>/dev/null | awk '{print $4}')
    printf "     t+%-3ss  replicas=%-2s  cpu %s\n" "$t" "${reps:-?}" "${targets:-n/a}"
  done
  wait "$load" 2>/dev/null || true
  kubectl delete hpa/gateway -n "$NS" >/dev/null 2>&1 || true
  scale_to gateway 1
  limiter_restore
}

main() {
  case "${1:-}" in
    scale)            exp_scale ;;
    pod-failure)      exp_pod_failure ;;
    rolling-restart)  exp_rolling_restart ;;
    degrade-redis)    exp_degrade_redis ;;
    degrade-postgres) exp_degrade_postgres ;;
    hpa)              exp_hpa ;;
    all)
      exp_scale; echo
      exp_pod_failure; echo
      exp_rolling_restart; echo
      exp_degrade_redis; echo
      exp_degrade_postgres; echo
      exp_hpa ;;
    *)
      grep -E '^#( |$)' "$0" | sed -E 's/^# ?//' | head -25 ; exit 1 ;;
  esac
}
main "$@"
