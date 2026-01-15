#!/usr/bin/env bash
set -e
echo "Initializing with arguments $@"
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y iproute2 util-linux docker.io
#/create_vnet.sh -n "$2" sw
touch "/tmp/vnet_ready/$1"
echo "vnet ready"
sleep infinity
