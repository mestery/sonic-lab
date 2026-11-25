#!/usr/bin/env bash
# verify-hosts.sh: end-to-end check of tgen's default host addressing.
#
# Usage: .github/scripts/verify-hosts.sh <lab-dir>
#
# Expects a running lab generated with hosts (-h) and the default
# addressing.txt. Everything is derived from that file, so the check is
# generator-driven like the BGP check in CI:
#
#   1. Apply addressing.txt with the lab's apply-addressing.sh.
#   2. Ping every ordered pair of hosts in each address family, retrying
#      until the fabric has advertised every host subnet.
#   3. Re-ping every pair strictly: no loss and no duplicate replies.
#   4. Re-address one host at runtime to an unused subnet index (remove,
#      edit, re-apply); check the old subnet is withdrawn and the new one
#      is advertised and reachable.
#
# Runs on bash 3.2 (macOS) as well as CI's bash, so it can be run locally.
#
# Environment: CONVERGE_TIMEOUT  seconds to wait for routes (default 300)

set -u

DIR="${1:?usage: $0 <lab-dir>}"
FILE="$DIR/addressing.txt"
APPLY="$DIR/apply-addressing.sh"
TIMEOUT="${CONVERGE_TIMEOUT:-300}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

[ -f "$FILE" ] || { echo "FAIL: $FILE not found (generate the lab with -h)"; exit 1; }

# Host rows are the ones with a gateway (4th column). Endpoints are
# "host address" pairs with the prefix length stripped.
hosts_endpoints() {
    awk '$1 !~ /^#/ && NF >= 4 { sub(/\/.*/, "", $3); print $1, $3 }' "$1"
}

same_family() {
    case "$1" in *:*) case "$2" in *:*) return 0 ;; esac; return 1 ;; esac
    case "$2" in *:*) return 1 ;; esac
    return 0
}

# ping_pairs <endpoints-file> <count> <strict>: ping every ordered pair of
# endpoints on different hosts in the same family. Non-strict: any reply
# counts. Strict: all replies received and no duplicates. Prints failures,
# returns 1 if any.
ping_pairs() {
    local eps="$1" count="$2" strict="$3" rc=0 out
    while read -r sh sa <&4; do
        while read -r dh da <&5; do
            [ "$sh" = "$dh" ] && continue
            same_family "$sa" "$da" || continue
            out=$(docker exec "$sh" ping -c "$count" -i 0.2 -W 2 "$da" 2>&1)
            if [ "$strict" = yes ]; then
                if echo "$out" | grep -q 'DUP!'; then
                    echo "  $sh -> $da: DUPLICATE replies"; rc=1
                elif ! echo "$out" | grep -q " $count received"; then
                    echo "  $sh -> $da: $(echo "$out" | grep 'transmitted')"; rc=1
                fi
            elif ! echo "$out" | grep -qE ' [1-9][0-9]* received'; then
                echo "  $sh -> $da: no reply"; rc=1
            fi
        done 5< "$eps"
    done 4< "$eps"
    return $rc
}

# wait_pairs <endpoints-file> <what>: retry ping_pairs until all pairs reply.
wait_pairs() {
    local start=$SECONDS
    until ping_pairs "$1" 1 no > "$TMP/last" 2>&1; do
        if [ $((SECONDS - start)) -ge "$TIMEOUT" ]; then
            echo "FAIL: $2: not all pairs reachable after ${TIMEOUT}s:"
            cat "$TMP/last"
            return 1
        fi
        sleep 5
    done
    echo "$2: all pairs reachable after $((SECONDS - start))s"
}

# wait_route <switch> <prefix> <present|absent>
wait_route() {
    local sw="$1" pfx="$2" want="$3" start=$SECONDS fam="" got
    case "$pfx" in *:*) fam="-6" ;; esac
    while :; do
        got=$(docker exec "sonic-$sw" ip $fam route show "$pfx" 2>/dev/null)
        if [ "$want" = present ] && [ -n "$got" ]; then break; fi
        if [ "$want" = absent ] && [ -z "$got" ]; then break; fi
        if [ $((SECONDS - start)) -ge "$TIMEOUT" ]; then
            echo "FAIL: $pfx should be $want on $sw after ${TIMEOUT}s"
            return 1
        fi
        sleep 3
    done
    echo "  $sw: $pfx $want"
}

# 1. Apply the starting state.
"$APPLY" "$FILE" || { echo "FAIL: apply-addressing.sh failed"; exit 1; }

hosts_endpoints "$FILE" > "$TMP/eps"
n=$(awk '{print $1}' "$TMP/eps" | sort -u | wc -l | tr -d ' ')
[ "$n" -ge 2 ] || { echo "FAIL: need at least 2 hosts in $FILE, found $n"; exit 1; }
echo "Checking $n hosts, $(wc -l < "$TMP/eps" | tr -d ' ') endpoints"

# 2-3. Full matrix: wait for convergence, then a strict pass.
wait_pairs "$TMP/eps" "initial addressing" || exit 1
ping_pairs "$TMP/eps" 5 yes || { echo "FAIL: loss or duplicate replies (see above)"; exit 1; }
echo "Strict pass: every pair 5/5, no duplicates"

# 4. Runtime re-address: move the first host's attachment from subnet
#    index H to 99 in both families.
h=$(awk 'NR==1 {print $1}' "$TMP/eps")
v4=$(awk -v h="$h" '$1==h && $2 !~ /:/ {print $2; exit}' "$TMP/eps")
v6=$(awk -v h="$h" '$1==h && $2 ~ /:/ {print $2; exit}' "$TMP/eps")
old4="${v4%.*}"               # 10.S.H
old6="${v6%::*}"              # fd00:0:S:H
# Pick a host-link index no other link on that switch uses (H counts up
# from 1, so start well above it and skip anything already in the file).
idx=99
while grep -qE "[[:space:]](${old4%.*}\.$idx\.|${old6%:*}:$idx::)" "$FILE"; do
    idx=$((idx + 1))
    [ "$idx" -le 255 ] || { echo "FAIL: no free host-link index on $h's switch"; exit 1; }
done
new4="${old4%.*}.$idx"
new6="${old6%:*}:$idx"
sw=$(awk -v gw="$old4.1/24" '$1 !~ /^#/ && $3==gw {print $1; exit}' "$FILE")
other=$(awk -v sw="$sw" '$1 !~ /^#/ && NF==3 && $1!=sw {print $1; exit}' "$FILE")
[ -n "$sw" ] && [ -n "$other" ] || { echo "FAIL: can't find $h's switch or another switch"; exit 1; }

echo "Re-addressing $h ($sw): $old4.0/24 -> $new4.0/24, $old6::/64 -> $new6::/64"
grep -E "[[:space:]]($old4\.|$old6::)" "$FILE" > "$TMP/old.txt"
sed -e "s/$old4\./$new4./g" -e "s/$old6::/$new6::/g" "$TMP/old.txt" > "$TMP/new.txt"
"$APPLY" -r "$TMP/old.txt" || { echo "FAIL: remove old addressing"; exit 1; }
"$APPLY" "$TMP/new.txt" || { echo "FAIL: apply new addressing"; exit 1; }

echo "Checking routes on $other:"
wait_route "$other" "$old4.0/24" absent || exit 1
wait_route "$other" "$old6::/64" absent || exit 1
wait_route "$other" "$new4.0/24" present || exit 1
wait_route "$other" "$new6::/64" present || exit 1

hosts_endpoints "$FILE" | grep -v "^$h " > "$TMP/eps2"
hosts_endpoints "$TMP/new.txt" >> "$TMP/eps2"
wait_pairs "$TMP/eps2" "after re-address" || exit 1
ping_pairs "$TMP/eps2" 5 yes || { echo "FAIL: loss or duplicate replies after re-address"; exit 1; }

echo "Host addressing verified: all pairs reachable in both families, no duplicates, runtime re-address works."
