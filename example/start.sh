#!/usr/bin/env bash
	set -e

	echo "Building images..."
	docker compose build

	echo "Starting topology..."
	docker compose up -d

	echo
	echo "Running containers:"
	docker ps --filter "name=sonic-"
	echo
	echo "Webmesh UI: http://localhost:8092"

	# Apply the default host addressing (the lab's starting state). This
	# waits for the switches to finish booting, which takes a few minutes.
	if [ -f addressing.txt ]; then
	echo
	./apply-addressing.sh addressing.txt
	fi
	