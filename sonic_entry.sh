#!/usr/bin/env bash
set -e

echo "Waiting for vnet file /tmp/vnet_ready/$1"

# Wait for vnet ready file in Debian container
while [ ! -f "/tmp/vnet_ready/$1" ]; do
    sleep 3
done

echo "vnet ready, starting SONiC..."
exec /usr/local/bin/supervisord -n
