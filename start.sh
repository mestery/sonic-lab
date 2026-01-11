#!/usr/bin/env bash
set -e

echo "Stopping existing topology..."
docker compose down -v || true

echo "Starting topology..."
docker compose up -d

echo
echo "Running containers:"
docker ps --filter "name=sonic-"

echo
echo "Tip:"
echo "  docker exec -it sonic-spine bash"

echo "Sleep 30..."
sleep 30

echo "Configuring spine ports."
docker exec -it sonic-spine config interface startup Ethernet0
docker exec -it sonic-spine config interface startup Ethernet4
docker exec -it sonic-spine config interface startup Ethernet8
docker exec -it sonic-spine config interface startup Ethernet12

echo "Configuring leaf1 ports."
docker exec -it sonic-leaf1 config interface startup Ethernet0
docker exec -it sonic-leaf1 config interface startup Ethernet4

echo "Configuring leaf2 ports."
docker exec -it sonic-leaf2 config interface startup Ethernet0
docker exec -it sonic-leaf2 config interface startup Ethernet4