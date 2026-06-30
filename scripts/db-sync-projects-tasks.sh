#!/usr/bin/env bash
# =============================================================================
# Selective sync: ADD local projects + tasks to prod for the two workspaces whose
# UUIDs match local<->prod (prod was seeded from local, then diverged):
#   b231c11f  Octane
#   356e2301  Sales Doctor (sd-main)
#
# ADDITIVE ONLY: every insert is ON CONFLICT DO NOTHING — it adds rows that are
# missing from prod and NEVER overwrites or deletes an existing prod row. Leaves
# runtimes / agents / users / members / workspaces untouched.
#
# Copies (FK order): user, member, issue_label, project, sprint, issue, comment,
# issue_to_label, issue_to_sprint, issue_dependency. Skips: issue_pull_request
# (needs GitHub PR rows), attachments (blob storage), operational tables
# (inbox/queue/activity), and AGENTS (kept as-is per "runtimes/agents stay").
#
# user + member are copied additively so issue assignees/creators resolve on prod
# (assignee_id/creator_id are polymorphic with NO FK, but the people behind them
# need to exist to render). Adding people does NOT touch runtimes or agents.
#
# Run from repo root:  bash scripts/db-sync-projects-tasks.sh
# =============================================================================
set -euo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"

WS="'b231c11f-ae45-4aab-8f31-0f5cfac6ddd7','356e2301-64fd-440b-ab68-7fcfa76088b1'"
C=agora-postgres-1
OUT="$HOME/agora-db-migrate"; mkdir -p "$OUT"
TS=$(date +%Y%m%d_%H%M%S)

LOCAL_PW=$(grep -E '^POSTGRES_PASSWORD=' .env | sed -E 's/^POSTGRES_PASSWORD=//')
echo "Fetching prod DB password..."
PROD_PW=$(fly ssh console -a sd-agora-db -C "printenv POSTGRES_PASSWORD" | tr -d '\r\n')
[ -n "$PROD_PW" ] || { echo "no prod pw"; exit 1; }
echo "Opening fly proxy to prod DB..."
fly proxy 16432:5432 -a sd-agora-db >/dev/null 2>&1 & PXP=$!
trap 'kill $PXP 2>/dev/null || true' EXIT
sleep 5

LP(){ docker exec -i -e PGPASSWORD="$LOCAL_PW" $C psql -U agora -d agora "$@"; }
PP(){ docker exec -i -e PGPASSWORD="$PROD_PW" $C psql -h host.docker.internal -p 16432 -U agora -d agora "$@"; }

echo "=== PROD counts BEFORE (the 2 workspaces) ==="
PP -c "select 'project' t,count(*) n from project where workspace_id in ($WS)
       union all select 'issue',count(*) from issue where workspace_id in ($WS)
       union all select 'comment',count(*) from comment where workspace_id in ($WS)
       union all select 'member',count(*) from member where workspace_id in ($WS)
       union all select 'user(total)',count(*) from \"user\";"

read -r -p "Add local projects+tasks to prod (ADDITIVE, never overwrites)? type YES: " ok
[ "$ok" = "YES" ] || { echo "aborted — prod untouched"; exit 1; }

# Fresh full prod backup as an undo point (additive is low risk, but cheap insurance).
BAK="$OUT/prod_presync_$TS.dump"
echo "Backing up prod -> $BAK"
docker exec -e PGPASSWORD="$PROD_PW" $C pg_dump -h host.docker.internal -p 16432 -U agora -d agora -Fc > "$BAK"
SZ=$(stat -f%z "$BAK" 2>/dev/null || stat -c%s "$BAK")
[ "$SZ" -gt 10000 ] || { echo "backup too small ($SZ) — ABORT"; exit 1; }
echo "  backup ok ($SZ bytes)"

# Column list = intersection(prod cols, local cols), in prod ordinal order, quoted.
# (Robust to schema drift; staging is created from the intersection so unprovided
# prod-only NOT NULL columns can't break COPY.)
build_cols(){
  local t="$1" pcols lcols c out=""
  pcols=$(PP -tA -c "select column_name from information_schema.columns where table_name='$t' order by ordinal_position")
  lcols=$(LP -tA -c "select column_name from information_schema.columns where table_name='$t'")
  while IFS= read -r c; do
    [ -z "$c" ] && continue
    printf '%s\n' "$lcols" | grep -qx "$c" && out="$out,\"$c\""
  done <<EOF
$pcols
EOF
  printf '%s' "${out#,}"
}

# sync TABLE WHERE [INSERT_FILTER]
#   WHERE         filters which local rows are staged (source side)
#   INSERT_FILTER extra guard applied on prod when inserting from staging
#                 (default true) — used so member only inserts when its user_id
#                 already exists on prod, avoiding an FK violation.
# Real table refs are quoted ("$t") because one of them ("user") is reserved.
sync(){
  local t="$1" where="$2" insfilter="${3:-true}" cols res
  cols=$(build_cols "$t")
  [ -n "$cols" ] || { echo "  !! $t: no shared columns — skipped"; return 0; }
  PP -q -c "drop table if exists _stg_$t; create table _stg_$t as select $cols from \"$t\" where false;" >/dev/null
  LP -c "\copy (select $cols from \"$t\" where $where) to stdout" | PP -q -c "\copy _stg_$t ($cols) from stdin"
  res=$(PP -tA -c "with ins as (insert into \"$t\" ($cols) select $cols from _stg_$t where $insfilter on conflict do nothing returning 1)
                   select (select count(*) from ins)||' / '||(select count(*) from _stg_$t);")
  PP -q -c "drop table _stg_$t;" >/dev/null
  echo "  $t: inserted / staged = $res"
}

ISS="select id from issue where workspace_id in ($WS)"
# Users referenced by members in these workspaces, or directly as issue
# creator/assignee with type 'user'.
USR="id in (select user_id from member where workspace_id in ($WS))
     or id in (select creator_id from issue where workspace_id in ($WS) and creator_type='user')
     or id in (select assignee_id from issue where workspace_id in ($WS) and assignee_type='user')"
echo "=== syncing local -> prod (FK order, additive) ==="
sync user             "$USR"
sync member           "workspace_id in ($WS)" "user_id in (select id from \"user\")"
sync issue_label      "workspace_id in ($WS)"
sync project          "workspace_id in ($WS)"
sync sprint           "workspace_id in ($WS)"
sync issue            "workspace_id in ($WS)"
# Filter children by their issue being in the copied set, and guard each insert
# so any orphan ref (cross-workspace issue_id, missing label/sprint) is skipped
# instead of raising an FK error.
sync comment          "issue_id in ($ISS)" \
                      "issue_id in (select id from issue)"
sync issue_to_label   "issue_id in ($ISS)" \
                      "issue_id in (select id from issue) and label_id in (select id from issue_label)"
sync issue_to_sprint  "issue_id in ($ISS)" \
                      "issue_id in (select id from issue) and sprint_id in (select id from sprint)"
sync issue_dependency "issue_id in ($ISS) and depends_on_issue_id in ($ISS)" \
                      "issue_id in (select id from issue) and depends_on_issue_id in (select id from issue)"

echo "=== PROD counts AFTER ==="
PP -c "select 'project' t,count(*) n from project where workspace_id in ($WS)
       union all select 'issue',count(*) from issue where workspace_id in ($WS)
       union all select 'comment',count(*) from comment where workspace_id in ($WS)
       union all select 'member',count(*) from member where workspace_id in ($WS)
       union all select 'user(total)',count(*) from \"user\";"
echo
echo "Done (additive — existing prod rows untouched)."
echo "  backup/undo : $BAK"
echo "NOT copied: PR links, attachments, operational rows (inbox/queue/activity_log),"
echo "            agents (kept as-is). Agent-assigned issues whose agent is local-only"
echo "            would show a missing assignee — rare (agents are mostly shared)."
echo "Added users have no auth identity, so they exist for assignee display but"
echo "can't log in until invited/linked normally."
