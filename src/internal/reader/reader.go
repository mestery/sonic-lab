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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Topology Topology `yaml:"topology"`
}

type Topology struct {
	Name     string            `yaml:"name"`
	Switches map[string]Switch `yaml:"switches"`
	Links    []Link            `yaml:"links"`
}

type Switch struct {
	Role  string                 `yaml:"role"`
	ASN   int                    `yaml:"asn"`
	Ports map[string]interface{} `yaml:"ports"`
}

type Link struct {
	Endpoints []Endpoint `yaml:"endpoints"`
}

type Endpoint struct {
	Device string `yaml:"device"`
	Port   string `yaml:"port"`
}

func Run(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	// Create docker-compose and create_vnet scripts
	generateDockerCompose(cfg)
	generateCreateVnet(cfg)

	return nil
}

func generateDockerCompose(cfg Config) {
	var sb strings.Builder

	sb.WriteString("version: \"3.9\"\n")
	sb.WriteString("x-sonic-common: &sonic-common\n  image: docker-sonic-vs:latest\n  platform: linux/amd64\n  privileged: true\n  tty: true\n  shm_size: \"2gb\"\n  security_opt:\n    - seccomp=unconfined\n\n")
	sb.WriteString("x-debian-common: &debian-common\n  image: debian:bullseye\n  platform: linux/amd64\n  privileged: true\n  tty: true\n  volumes:\n    - ./init_linux.sh:/init_linux.sh:ro\n    - ./create_vnet.sh:/create_vnet.sh:ro\n    - /var/run/docker.sock:/var/run/docker.sock\n    - ./tmp/vnet_ready:/tmp/vnet_ready:rw\n  shm_size: \"2gb\"\n\n")

	sb.WriteString("services:\n\t###########################\n\t# Backing Linux containers #\n\t###########################\n\n")

	keys := make([]string, 0, len(cfg.Topology.Switches))
	for k := range cfg.Topology.Switches {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		sw := cfg.Topology.Switches[name]
		portCount := len(sw.Ports)

		sb.WriteString(fmt.Sprintf("  %s-linux:\n", name))
		sb.WriteString("    <<: *debian-common\n")
		sb.WriteString(fmt.Sprintf("    container_name: %s-linux\n", name))
		sb.WriteString(fmt.Sprintf("    command: [\"bash\", \"/init_linux.sh\", \"%[1]s-linux\", \"%[2]d\"]\n\n", name, portCount))
	}

	sb.WriteString("\t###########################\n\t# SONiC VS containers      #\n\t###########################\n\n")

	for _, name := range keys {
		sb.WriteString(fmt.Sprintf("  %s:\n", name))
		sb.WriteString("    <<: *sonic-common\n")
		sb.WriteString(fmt.Sprintf("    container_name: sonic-%s\n", name))
		sb.WriteString("    depends_on:\n")
		sb.WriteString(fmt.Sprintf("      - %s-linux\n", name))
		sb.WriteString(fmt.Sprintf("    network_mode: \"service:%s-linux\"\n", name))
		sb.WriteString("    volumes:\n")
		sb.WriteString("      - ./tmp/vnet_ready:/tmp/vnet_ready:ro\n")
		sb.WriteString(fmt.Sprintf("      - /var/run/redis-vs/%s:/var/run/redis\n", name))
		sb.WriteString("      - ./sonic_entry.sh:/sonic_entry.sh:ro\n")
		sb.WriteString(fmt.Sprintf("    entrypoint: [\"/bin/bash\", \"/sonic_entry.sh\", \"%s-linux\"]", name))
	}

	writeDataToFile([]byte(sb.String()), "docker-compose.yml")
}

func generateCreateVnet(cfg Config) {
	var sb strings.Builder

	sb.WriteString("#!/usr/bin/env bash\nset -euo pipefail\n\n")
	sb.WriteString("# ----------------------------\n")
	sb.WriteString("# SONiC VS switch-to-switch veth setup\n")
	sb.WriteString("# ----------------------------\n\n")

	for name := range cfg.Topology.Switches {
		varName := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		sb.WriteString(varName + "=" + name + "\n")
	}
	sb.WriteString("\n")

	sb.WriteString("get_pid() {\n  docker inspect -f '{{.State.Pid}}' \"$1\"\n}\n\n")

	sb.WriteString("link_exists() {\n  local pid=$1\n  local ifname=$2\n  nsenter -t \"$pid\" -n ip link show \"$ifname\" &>/dev/null\n}\n\n")

	sb.WriteString("create_link() {\n  local a=$1\n  local b=$2\n  local a_if=$3\n  local b_if=$4\n  local a_pid b_pid\n\n")
	sb.WriteString("  a_pid=$(get_pid \"$a\")\n  b_pid=$(get_pid \"$b\")\n\n")
	sb.WriteString("  if link_exists \"$a_pid\" \"$a_if\" || link_exists \"$b_pid\" \"$b_if\"; then\n")
	sb.WriteString("    echo \"Skipping existing link $a:$a_if <-> $b:$b_if\"\n    return\n  fi\n\n")
	sb.WriteString("  local ha=\"v${a:0:3}${a_if#Ethernet}\"\n")
	sb.WriteString("  local hb=\"v${b:0:3}${b_if#Ethernet}\"\n")
	sb.WriteString("  echo \"Creating $a:$a_if <-> $b:$b_if\"\n")

	sb.WriteString("  ip link add \"$ha\" type veth peer name \"$hb\"\n")
	sb.WriteString("  ip link set \"$ha\" netns \"$a_pid\"\n")
	sb.WriteString("  ip link set \"$hb\" netns \"$b_pid\"\n")

	sb.WriteString("  nsenter -t \"$a_pid\" -n ip link set \"$ha\" name \"$a_if\"\n")
	sb.WriteString("  nsenter -t \"$b_pid\" -n ip link set \"$hb\" name \"$b_if\"\n")
	sb.WriteString("  nsenter -t \"$a_pid\" -n ip link set \"$a_if\" up\n")
	sb.WriteString("  nsenter -t \"$b_pid\" -n ip link set \"$b_if\" up\n}\n\n")

	sb.WriteString("echo \"Creating topology links...\"\n")

	for _, link := range cfg.Topology.Links {
		if len(link.Endpoints) == 2 {
			e1 := link.Endpoints[0]
			e2 := link.Endpoints[1]
			line := "create_link \"" + e1.Device + "\" \"" + e2.Device + "\" \"" + e1.Port + "\" \"" + e2.Port + "\"\n"
			sb.WriteString(line)
		}
	}

	sb.WriteString("Topology Complete!\n")
	writeDataToFile([]byte(sb.String()), "create_vnet.sh")
}

func writeDataToFile(data []byte, startScript string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	desktopPath := filepath.Join(homeDir, "Desktop/sonic-lab")
	filePath := filepath.Join(desktopPath, startScript)

	subString := ".sh"
	containsSh1 := strings.Contains(startScript, subString)

	if containsSh1 { // .sh file (executable)
		err = os.WriteFile(filePath, data, 0755)
		if err != nil {
			log.Fatal(err)
		}
	} else { // yml file
		err = os.WriteFile(filePath, data, 0644)
		if err != nil {
			log.Fatal(err)
		}
	}
}
