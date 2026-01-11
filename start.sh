#!/usr/bin/env bash
set -e

OPTSTRING=":l"

while getopts ":l" opt; do
    case "$opt" in
        l)
            echo "Loading docker-sonic-vs image..."
            wget -P /tmp https://artprodcus3.artifacts.visualstudio.com/Af91412a5-a906-4990-9d7c-f697b81fc04d/be1b070f-be15-4154-aade-b1d3bfb17054/_apis/artifact/cGlwZWxpbmVhcnRpZmFjdDovL21zc29uaWMvcHJvamVjdElkL2JlMWIwNzBmLWJlMTUtNDE1NC1hYWRlLWIxZDNiZmIxNzA1NC9idWlsZElkLzEwMTQ0OTYvYXJ0aWZhY3ROYW1lL3NvbmljLWJ1aWxkaW1hZ2UudnM1/content?format=file&subpath=/target/docker-sonic-vs.gz
            docker load < /tmp/docker-sonic-vs.gz
            ;;
        ?)
            echo "Invalid option: -${OPTARG}."
            exit 1
            ;;
        esac
    done

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