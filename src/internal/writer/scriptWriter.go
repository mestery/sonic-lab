/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package internal

import (
	"fmt"
	"log"
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
	files := []string{
		"create_vnet.sh",
		"docker-compose.yml",
	}

	if err := os.MkdirAll(w.OutputDir, 0o755); err != nil {
		return err
	}

	writeStartScript("startt.sh")
	writeEntryScript("sonic_entryy.sh")
	writeInitScript("init_linuxx.sh")

	for _, name := range files {
		if err := w.writeBlank(name); err != nil {
			return err
		}
	}

	return nil
}

func (w *Writer) writeBlank(name string) error {
	path := filepath.Join(w.OutputDir, name)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("failed creating %s: %w", path, err)
	}

	return file.Close()
}

func writeInitScript(initScript string) {
	data := []byte(`#!/usr/bin/env bash
set -e
echo "Initializing with arguments $@"
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y iproute2 util-linux docker.io
#/create_vnet.sh -n "$2" sw
touch "/tmp/vnet_ready/$1"
echo "vnet ready"
sleep infinity`)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	desktopPath := filepath.Join(homeDir, "Desktop/sonic-lab")
	filePath := filepath.Join(desktopPath, initScript)

	err = os.WriteFile(filePath, data, 0755)
	if err != nil {
		log.Fatal(err)
	}
}

func writeEntryScript(entryScript string) {
	data := []byte(`#!/usr/bin/env bash
set -e

echo "Waiting for vnet file /tmp/vnet_ready/$1"

# Wait for vnet ready file in Debian container
while [ ! -f "/tmp/vnet_ready/$1" ]; do
    sleep 3
done

echo "vnet ready, starting SONiC..."
exec /usr/local/bin/supervisord -n`)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	desktopPath := filepath.Join(homeDir, "Desktop/sonic-lab")
	filePath := filepath.Join(desktopPath, entryScript)

	err = os.WriteFile(filePath, data, 0755)
	if err != nil {
		log.Fatal(err)
	}
}

func writeStartScript(startScript string) {
	data := []byte(`#!/usr/bin/env bash
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
docker ps --filter "name=sonic-"`)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	desktopPath := filepath.Join(homeDir, "Desktop/sonic-lab")
	filePath := filepath.Join(desktopPath, startScript)

	err = os.WriteFile(filePath, data, 0755)
	if err != nil {
		log.Fatal(err)
	}
}
