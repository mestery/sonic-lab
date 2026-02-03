#!/usr/bin/env bash
set -euo pipefail

# ----------------------------
# SONiC VS switch-to-switch veth setup
# ----------------------------

SPINE1=spine1
LEAF1=leaf1
LEAF2=leaf2

get_pid() {
  docker inspect -f '{{.State.Pid}}' "$1"
}

link_exists() {
  local pid=$1
  local ifname=$2
  nsenter -t "$pid" -n ip link show "$ifname" &>/dev/null
}

create_link() {
  local a=$1
  local b=$2
  local a_if=$3
  local b_if=$4
  local a_pid b_pid

  a_pid=$(get_pid "$a")
  b_pid=$(get_pid "$b")

  if link_exists "$a_pid" "$a_if" || link_exists "$b_pid" "$b_if"; then
    echo "Skipping existing link $a:$a_if <-> $b:$b_if"
    return
  fi

  local ha="v${a:0:3}${a_if#Ethernet}"
  local hb="v${b:0:3}${b_if#Ethernet}"
  echo "Creating $a:$a_if <-> $b:$b_if"
  ip link add "$ha" type veth peer name "$hb"
  ip link set "$ha" netns "$a_pid"
  ip link set "$hb" netns "$b_pid"
  nsenter -t "$a_pid" -n ip link set "$ha" name "$a_if"
  nsenter -t "$b_pid" -n ip link set "$hb" name "$b_if"
  nsenter -t "$a_pid" -n ip link set "$a_if" up
  nsenter -t "$b_pid" -n ip link set "$b_if" up
}

echo "Creating topology links..."
create_link "spine1" "leaf1" "Ethernet0" "Ethernet0"
create_link "spine1" "leaf1" "Ethernet4" "Ethernet4"
create_link "spine1" "leaf2" "Ethernet8" "Ethernet0"
create_link "spine1" "leaf2" "Ethernet12" "Ethernet4"
Topology Complete!
