#!/usr/bin/env bash
set -e

echo "Stopping existing topology..."
docker compose down -v || true

echo "Pulling amd64 SONiC image (via Rosetta)..."
docker pull --platform linux/amd64 docker-sonic-vs:latest

echo "Starting topology..."
docker compose up -d

echo
echo "Running containers:"
docker ps --filter "name=sonic-"

echo
echo "Tip:"
echo "  docker exec -it sonic-spine bash"

