#!/usr/bin/env bash
# Seed an unlocked marketing capsule "Summertides 2026" with engineered Recap stats.
# Idempotent: deletes any previous capsule with that name before re-inserting.
#
# Usage (from memoria-gobackend/):
#   ./scripts/seed_summertides_recap.sh
#   make seed-summertides
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.yml"
API="${BASE_URL:-http://localhost:8080}/v1"
CAPSULE_NAME="Summertides 2026"
SEED_PASSWORD="${SEED_PASSWORD:-summertides2026}"
TZ_NAME="Africa/Nairobi"
TMPDIR_SEED="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_SEED"' EXIT

cd "$ROOT"
export PATH="${HOME}/.local/go/bin:${PATH}"

echo "== Ensuring docker stack is up =="
docker compose -f "$COMPOSE_FILE" up -d >/dev/null
for _ in $(seq 1 60); do
  if curl -sf http://localhost:8080/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
curl -sf http://localhost:8080/healthz >/dev/null

psql_q() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -U memoria -d memoria -v ON_ERROR_STOP=1 "$@"
}

# Run SQL from stdin (host files are not mounted in the postgres container).
psql_stdin() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -U memoria -d memoria -v ON_ERROR_STOP=1 "$@"
}

echo "== Generating password hash =="
HASH="$(go run ./scripts/hashpwd/main.go "$SEED_PASSWORD")"
printf '%s' "$HASH" > "$TMPDIR_SEED/hash.txt"

echo "== Resolving target user =="
# Prefer username demo_user, else email you@example.com, else create.
TARGET_JSON="$(psql_q -tAc "
SELECT COALESCE(
  (SELECT json_build_object('id', id::text, 'username', username::text, 'email', email::text, 'display_name', display_name, 'created', false)
   FROM users WHERE deleted_at IS NULL AND username = 'demo_user' LIMIT 1),
  (SELECT json_build_object('id', id::text, 'username', username::text, 'email', email::text, 'display_name', display_name, 'created', false)
   FROM users WHERE deleted_at IS NULL AND email = 'you@example.com' LIMIT 1),
  'null'::json
);
")"

CREATED_USER=0
if [[ "$TARGET_JSON" == "null" || -z "$TARGET_JSON" ]]; then
  echo "   Creating user demo_user..."
  CREATED_USER=1
  # Write SQL file to avoid shell expansion of $ in argon2 hash
  python3 - "$TMPDIR_SEED" "$HASH" "$TZ_NAME" <<'PY'
import pathlib, sys
outdir, hash_, tz = sys.argv[1], sys.argv[2], sys.argv[3]
# Escape single quotes for SQL string literal
h = hash_.replace("'", "''")
pathlib.Path(outdir, "create_prince.sql").write_text(f"""
INSERT INTO users (email, username, display_name, password_hash, timezone, plan)
VALUES (
  'demo_user@memoria.local',
  'demo_user',
  'Prince',
  '{h}',
  '{tz}',
  'spark'
);
SELECT json_build_object(
  'id', id::text,
  'username', username::text,
  'email', email::text,
  'display_name', display_name,
  'created', true
) FROM users WHERE username = 'demo_user' AND deleted_at IS NULL;
""")
PY
  TARGET_JSON="$(psql_stdin -tAc < "$TMPDIR_SEED/create_prince.sql" | tail -1)"
fi

PRINCE_ID="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['id'])" "$TARGET_JSON")"
PRINCE_USER="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['username'])" "$TARGET_JSON")"
PRINCE_EMAIL="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['email'])" "$TARGET_JSON")"

# Marketing display name + timezone for Recap pills / local hours.
# Always set seed password so local login works for screenshots.
python3 - "$TMPDIR_SEED" "$HASH" "$PRINCE_ID" "$TZ_NAME" <<'PY'
import pathlib, sys
outdir, hash_, uid, tz = sys.argv[1:5]
h = hash_.replace("'", "''")
pathlib.Path(outdir, "update_prince.sql").write_text(f"""
UPDATE users
SET display_name = 'Prince',
    timezone = '{tz}',
    password_hash = '{h}'
WHERE id = '{uid}'::uuid;
""")
PY
psql_stdin < "$TMPDIR_SEED/update_prince.sql" >/dev/null

echo "   Target: @$PRINCE_USER <$PRINCE_EMAIL> ($PRINCE_ID)"

echo "== Upserting friend cast =="
python3 - "$TMPDIR_SEED" "$HASH" "$TZ_NAME" <<'PY'
import pathlib, sys
outdir, hash_, tz = sys.argv[1:4]
h = hash_.replace("'", "''")
friends = [
    ("whitney_summertides", "Whitney"),
    ("obugi_summertides", "Obugi"),
    ("reneeey_summertides", "Reneeey"),
    ("cy_summertides", "Cy"),
    ("samantha_summertides", "Samantha"),
    ("kim_summertides", "Kim"),
    ("ojuka_summertides", "Ojuka"),
    ("mark_summertides", "Mark"),
]
parts = ["BEGIN;"]
for uname, dname in friends:
    email = f"{uname}@memoria.local"
    parts.append(f"""
INSERT INTO users (email, username, display_name, password_hash, timezone, plan)
SELECT '{email}', '{uname}', '{dname}', '{h}', '{tz}', 'spark'
WHERE NOT EXISTS (
  SELECT 1 FROM users WHERE deleted_at IS NULL AND (username = '{uname}' OR email = '{email}')
);
UPDATE users
SET display_name = '{dname}', timezone = '{tz}', password_hash = '{h}'
WHERE deleted_at IS NULL AND username = '{uname}';
""")
parts.append("COMMIT;")
pathlib.Path(outdir, "friends.sql").write_text("\n".join(parts))
PY
psql_stdin < "$TMPDIR_SEED/friends.sql" >/dev/null

# Load friend IDs
while IFS='|' read -r dname fid; do
  case "$dname" in
    Whitney) WHITNEY_ID="$fid" ;;
    Obugi) OBUGI_ID="$fid" ;;
    Reneeey) RENEEEY_ID="$fid" ;;
    Cy) CY_ID="$fid" ;;
    Samantha) SAMANTHA_ID="$fid" ;;
    Kim) KIM_ID="$fid" ;;
    Ojuka) OJUKA_ID="$fid" ;;
    Mark) MARK_ID="$fid" ;;
  esac
  echo "   $dname → $fid"
done < <(psql_q -tAc "
SELECT display_name || '|' || id::text
FROM users
WHERE deleted_at IS NULL AND username IN (
  'whitney_summertides','obugi_summertides','reneeey_summertides','cy_summertides',
  'samantha_summertides','kim_summertides','ojuka_summertides','mark_summertides'
)
ORDER BY display_name;
")

echo "== Removing previous '$CAPSULE_NAME' capsules =="
psql_q -c "
DO \$\$
DECLARE
  cap uuid;
BEGIN
  FOR cap IN
    SELECT id FROM capsules WHERE name = '$CAPSULE_NAME'
  LOOP
    DELETE FROM reactions
    WHERE memory_id IN (
      SELECT id FROM memories WHERE container_type = 'capsule' AND container_id = cap
    );
    DELETE FROM comments
    WHERE memory_id IN (
      SELECT id FROM memories WHERE container_type = 'capsule' AND container_id = cap
    );
    DELETE FROM memory_idempotency WHERE capsule_id = cap;
    DELETE FROM media
    WHERE id IN (
      SELECT media_id FROM memories WHERE container_type = 'capsule' AND container_id = cap
      UNION
      SELECT voice_media_id FROM memories
      WHERE container_type = 'capsule' AND container_id = cap AND voice_media_id IS NOT NULL
    );
    DELETE FROM memories WHERE container_type = 'capsule' AND container_id = cap;
    DELETE FROM capsule_unfreeze_votes WHERE capsule_id = cap;
    DELETE FROM streak_days WHERE capsule_id = cap;
    DELETE FROM capsule_members WHERE capsule_id = cap;
    DELETE FROM capsules WHERE id = cap;
  END LOOP;
END \$\$;
" >/dev/null

echo "== Creating unlocked capsule =="
CAPSULE_ID="$(psql_q -tAc "
INSERT INTO capsules (
  creator_id, name, description, type, state,
  unlock_at, unlocked_at, viewable_until,
  streak_current, streak_perfect, last_contribution_at,
  created_at
) VALUES (
  '$PRINCE_ID'::uuid,
  '$CAPSULE_NAME',
  'Marketing seed — Summertides crew trip',
  'group',
  'unlocked',
  '2026-07-06 09:00:00+00',
  '2026-07-06 09:00:00+00',
  NULL,
  1,
  true,
  '2026-07-05 23:30:00+00',
  '2026-07-02 10:00:00+00'
)
RETURNING id::text;
" | head -1 | tr -d '[:space:]')"

if [[ ! "$CAPSULE_ID" =~ ^[0-9a-f-]{36}$ ]]; then
  echo "ERROR: bad capsule id: '$CAPSULE_ID'" >&2
  exit 1
fi
echo "   Capsule: $CAPSULE_ID"

psql_q -c "
INSERT INTO capsule_members (capsule_id, user_id, role, invite_status, accepted_at, view_blocked, unlock_reveal_seen_at)
VALUES
  ('$CAPSULE_ID'::uuid, '$PRINCE_ID'::uuid, 'admin', 'accepted', '2026-07-02 10:00:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$WHITNEY_ID'::uuid, 'member', 'accepted', '2026-07-02 10:05:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$OBUGI_ID'::uuid, 'member', 'accepted', '2026-07-02 10:06:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$RENEEEY_ID'::uuid, 'member', 'accepted', '2026-07-02 10:07:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$CY_ID'::uuid, 'member', 'accepted', '2026-07-02 10:08:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$SAMANTHA_ID'::uuid, 'member', 'accepted', '2026-07-02 10:09:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$KIM_ID'::uuid, 'member', 'accepted', '2026-07-02 10:10:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$OJUKA_ID'::uuid, 'member', 'accepted', '2026-07-02 10:11:00+00', false, NULL),
  ('$CAPSULE_ID'::uuid, '$MARK_ID'::uuid, 'member', 'accepted', '2026-07-02 10:12:00+00', false, NULL);

INSERT INTO streak_days (capsule_id, day, contributor_count) VALUES
  ('$CAPSULE_ID'::uuid, '2026-07-02', 4),
  ('$CAPSULE_ID'::uuid, '2026-07-03', 5),
  ('$CAPSULE_ID'::uuid, '2026-07-04', 7),
  ('$CAPSULE_ID'::uuid, '2026-07-05', 4);
" >/dev/null

echo "== Building 44 memories with engineered capture times =="
python3 - "$CAPSULE_ID" "$PRINCE_ID" "$WHITNEY_ID" "$OBUGI_ID" "$RENEEEY_ID" "$CY_ID" "$SAMANTHA_ID" "$KIM_ID" "$OJUKA_ID" "$TMPDIR_SEED" <<'PY'
import sys
import uuid
from collections import Counter
from datetime import datetime, timezone
from zoneinfo import ZoneInfo

cap, prince, whitney, obugi, reneeey, cy, samantha, kim, ojuka, outdir = sys.argv[1:11]
EAT = ZoneInfo("Africa/Nairobi")

authors = (
    [whitney] * 20
    + [prince] * 7
    + [obugi] * 5
    + [reneeey] * 4
    + [cy] * 3
    + [samantha] * 2
    + [kim] * 2
    + [ojuka] * 1
)
assert len(authors) == 44

slots = []
# July 4 — 18 (busiest), peak hour 23
slots += [(7, 4, 23, m) for m in range(0, 12)]
slots += [(7, 4, 22, m) for m in (0, 15, 30, 45)]
slots += [(7, 4, 14, m) for m in (0, 20)]
# July 2 — 8
slots += [(7, 2, 23, m) for m in (10, 40)]
slots += [(7, 2, 18, m) for m in (0, 30)]
slots += [(7, 2, 16, m) for m in (0, 45)]
slots += [(7, 2, 0, m) for m in (20, 50)]
# July 3 — 10
slots += [(7, 3, 1, 10), (7, 3, 2, 20)]
slots += [(7, 3, 11, m) for m in (0, 30, 50)]
slots += [(7, 3, 15, m) for m in (0, 25, 50)]
slots += [(7, 3, 19, m) for m in (0, 40)]
# July 5 — 8
slots += [(7, 5, 23, m) for m in (5, 35)]
slots += [(7, 5, 18, m) for m in (10, 40, 55)]
slots += [(7, 5, 16, 0), (7, 5, 14, 30), (7, 5, 19, 15)]
assert len(slots) == 44, len(slots)

video_idxs = {0, 3, 20, 25}

# Minimal valid JPEG
JPEG = bytes.fromhex(
    "ffd8ffe000104a46494600010100000100010000ffdb004300080606070605080707070909080a0c"
    "140d0c0b0b0c1912130f141d1a1f1e1d1a1c1c20242e2720222c231c1c2837292c30313434341f27"
    "393d38323c2e333432ffc0000b080001000101011100ffc4001f0000010501010101010100000000"
    "000000000102030405060708090a0bffc400b5100002010303020403050504040000017d01020300"
    "041105122131410613516107227114328191a1082342b1c11552d1f02433627282090a161718191a"
    "25262728292a3435363738393a434445464748494a535455565758595a636465666768696a737475"
    "767778797a838485868788898a92939495969798999aa2a3a4a5a6a7a8a9aab2b3b4b5b6b7b8b9ba"
    "c2c3c4c5c6c7c8c9cad2d3d4d5d6d7d8d9dae1e2e3e4e5e6e7e8e9eaf1f2f3f4f5f6f7f8f9faffda"
    "0008010100003f007f46ffd9"
)

rows = []
with open(f"{outdir}/manifest.tsv", "w") as man:
    for i, (author, slot) in enumerate(zip(authors, slots)):
        mid = str(uuid.uuid4())
        mem_id = str(uuid.uuid4())
        month, day, hour, minute = slot
        local = datetime(2026, month, day, hour, minute, tzinfo=EAT)
        captured = local.astimezone(timezone.utc)
        is_video = i in video_idxs
        kind = "video" if is_video else "photo"
        ctype = "video/mp4" if is_video else "image/jpeg"
        key = f"media/{author}/{mid}"
        fpath = f"{outdir}/{mid}.bin"
        with open(fpath, "wb") as f:
            f.write(JPEG)
        man.write(f"{key}\t{fpath}\t{ctype}\n")
        w, h = (1280, 720) if is_video else (1080, 1440)
        dur = 5000 if is_video else None
        rows.append((mid, mem_id, author, kind, ctype, key, w, h, dur, len(JPEG), captured, i))

hot_memory = rows[0][1]
open(f"{outdir}/hot_memory.txt", "w").write(hot_memory)

with open(f"{outdir}/seed.sql", "w") as sql:
    sql.write("BEGIN;\n")
    for mid, mem_id, author, kind, ctype, key, w, h, dur, size, captured, i in rows:
        dur_sql = "NULL" if dur is None else str(dur)
        ts = captured.strftime("%Y-%m-%d %H:%M:%S+00")
        sql.write(
            f"INSERT INTO media (id, owner_id, kind, bucket_key, content_type, duration_ms, width, height, byte_size, status, created_at)\n"
            f"VALUES ('{mid}'::uuid, '{author}'::uuid, '{kind}', '{key}', '{ctype}', {dur_sql}, {w}, {h}, {size}, 'ready', '{ts}'::timestamptz);\n"
        )
        sql.write(
            f"INSERT INTO memories (id, container_type, container_id, author_id, media_id, caption, created_at, captured_at)\n"
            f"VALUES ('{mem_id}'::uuid, 'capsule', '{cap}'::uuid, '{author}'::uuid, '{mid}'::uuid, NULL, '{ts}'::timestamptz, '{ts}'::timestamptz);\n"
        )
    sql.write("COMMIT;\n")

hours = Counter(s[2] for s in slots)
days = Counter(s[1] for s in slots)
night = sum(1 for s in slots if s[2] >= 22 or s[2] < 5)
print(f"wrote {len(rows)} memories; hot={hot_memory}")
print("peak_hour", hours.most_common(1)[0], "night", night, "jul4", days[4])
PY

psql_stdin < "$TMPDIR_SEED/seed.sql" >/dev/null

echo "== Skipping reactions (can't react before unlock — Recap must not show reaction beats) =="

echo "== Uploading placeholder media to MinIO =="
UPLOAD_OK=0
if docker run --rm --network memoria-gobackend_default \
  --entrypoint /bin/sh \
  -v "$TMPDIR_SEED:/data" \
  minio/mc:latest -c '
    mc alias set local http://minio:9000 memoria memoria-secret >/dev/null &&
    mc mb -p local/memoria-media >/dev/null 2>&1 || true
    while IFS="$(printf "\t")" read -r key fpath ctype; do
      base=$(basename "$fpath")
      mc cp --attr "Content-Type=$ctype" "/data/$base" "local/memoria-media/$key" >/dev/null
    done < /data/manifest.tsv
  '; then
  UPLOAD_OK=1
  echo "   Uploaded placeholder objects"
else
  echo "   WARN: MinIO upload failed — Recap still works; viewer thumbs may 404"
fi

echo "== Contribution breakdown =="
psql_q -c "
SELECT u.display_name, COUNT(*) AS memories
FROM memories m
JOIN users u ON u.id = m.author_id
WHERE m.container_type = 'capsule' AND m.container_id = '$CAPSULE_ID'::uuid
GROUP BY u.display_name
ORDER BY memories DESC;
"

echo "== Login + fetch Recap =="
LOGIN_RESP="$(curl -s -X POST "$API/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$PRINCE_EMAIL\",\"password\":\"$SEED_PASSWORD\"}")"
ACCESS="$(python3 -c "
import json,sys
d=json.loads(sys.argv[1])
print((d.get('tokens') or {}).get('access_token') or '')
" "$LOGIN_RESP")"
if [[ -z "$ACCESS" ]]; then
  echo "ERROR: login failed: $LOGIN_RESP" >&2
  exit 1
fi

RECAP="$(curl -sf -H "Authorization: Bearer $ACCESS" "$API/capsules/$CAPSULE_ID/recap")"
echo "$CAPSULE_ID" > "$ROOT/scripts/.summertides_capsule_id"
echo "$RECAP" > "$ROOT/scripts/.summertides_recap.json"

python3 -c "
import json,sys
r=json.loads(sys.argv[1])
facts=r.get('facts') or {}
top=r.get('top_contributor') or {}
viewer=r.get('viewer') or {}
print('--- Recap facts ---')
print('name:', r.get('capsule_name'))
print('members:', r.get('member_count'), 'contributors:', r.get('contributor_count'))
print('memories:', r.get('total_memories'), 'photos:', r.get('photos'), 'videos:', r.get('videos'), 'voice:', r.get('voice_notes'))
print('days_sealed:', r.get('days_sealed'))
print('top:', top.get('display_name'), top.get('memory_count'), 'share', round((top.get('share_pct') or 0)*100), '%')
print('viewer memories:', viewer.get('memories_taken'), 'waiting:', viewer.get('memories_waiting'), 'rank:', viewer.get('contributor_rank'))
print('peak_hour:', r.get('peak_hour_local'), 'night_owl:', r.get('night_owl_share'))
print('busiest_day:', r.get('busiest_day'), r.get('busiest_day_count'))
print('most_reacted:', facts.get('most_reacted_count'), facts.get('most_reacted_author_name'))
" "$RECAP"

cat <<EOF

========================================
Summertides 2026 seed ready
========================================
Capsule ID:   $CAPSULE_ID
Open in app:  /(app)/capsule/$CAPSULE_ID
Replay Recap: /(app)/capsule/$CAPSULE_ID?celebrate=1
              (or Capsule → Stats → Replay story)

Login (local):
  email:    $PRINCE_EMAIL
  username: @$PRINCE_USER
  password: $SEED_PASSWORD

TikTok hook:
  We let AI takeover the Recap at Memoria… we literallyy cryiiing

Slide overlays:
  1 Intro        — Summertides 2026 🔥 / didn't even have time to collect dust
  2 Hook         — Somebody's about to get exposed
  3 Reveal       — 44 moments / 0 voice notes
  4 Punch        — One person took nearly half… Whitney, we need to talk
  5 Night        — % captured between 10pm and 5am / not a wellness retreat
  6 Warm         — No roast here
  7 CTA          — download Memoria before your friends seal one without you

MinIO upload: $UPLOAD_OK (1=ok)
========================================
EOF
