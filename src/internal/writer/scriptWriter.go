/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package writer

import (
	"fmt"
	"os"
	"path/filepath"
)

type Writer struct {
	OutputDir string
}

func New(outputDir string) *Writer {
	return &Writer{
		OutputDir: outputDir,
	}
}

func (w *Writer) Run() error {
	if err := os.MkdirAll(w.OutputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %q: %w", w.OutputDir, err)
	}

	if err := w.writeStartScript(); err != nil {
		return fmt.Errorf("writing start.sh: %w", err)
	}

	if err := w.writeEntryScript(); err != nil {
		return fmt.Errorf("writing sonic_entry.sh: %w", err)
	}

	if err := w.writeInitScript(); err != nil {
		return fmt.Errorf("writing init_linux.sh: %w", err)
	}

	return nil
}

func (w *Writer) writeInitScript() error {
	data := []byte(`#!/usr/bin/env bash
set -e
echo "Initializing with arguments $@"
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y iproute2 util-linux docker.io
#/create_vnet.sh -n "$2" sw
touch "/tmp/vnet_ready/$1"
echo "vnet ready"
sleep infinity
`)

	return os.WriteFile(filepath.Join(w.OutputDir, "init_linux.sh"), data, 0o755)
}

func (w *Writer) writeEntryScript() error {
	data := []byte(`#!/usr/bin/env bash
set -e

echo "Waiting for vnet file /tmp/vnet_ready/$1"

# Wait for vnet ready file in Debian container
while [ ! -f "/tmp/vnet_ready/$1" ]; do
    sleep 3
done

echo "vnet ready, starting SONiC..."
exec /usr/local/bin/supervisord -n
`)

	return os.WriteFile(filepath.Join(w.OutputDir, "sonic_entry.sh"), data, 0o755)
}

func (w *Writer) writeStartScript() error {
	data := []byte(`#!/usr/bin/env bash
set -e

OPTSTRING=":l"

while getopts ":l" opt; do
    case "$opt" in
        l)
            echo "Loading docker-sonic-vs image..."
            wget -P -o docker-sonic-vs.gz /tmp "https://artprodcus3.artifacts.visualstudio.com/Af91412a5-a906-4990-9d7c-f697b81fc04d/be1b070f-be15-4154-aade-b1d3bfb17054/_apis/artifact/cGlwZWxpbmVhcnRpZmFjdDovL21zc29uaWMvcHJvamVjdElkL2JlMWIwNzBmLWJlMTUtNDE1NC1hYWRlLWIxZDNiZmIxNzA1NC9idWlsZElkLzExOTc5NTIvYXJ0aWZhY3ROYW1lL3NvbmljLWJ1aWxkaW1hZ2UudnM1/content?format=file&subpath=/target/docker-sonic-vs.gz"
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
`)

	return os.WriteFile(filepath.Join(w.OutputDir, "start.sh"), data, 0o755)
}
