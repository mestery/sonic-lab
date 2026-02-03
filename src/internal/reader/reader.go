/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package internal

import (
	"log"
	"os"
	"path/filepath"
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
	data := []byte(`#!/usr/bin/env bash
set -euo pipefail`)

	writeDataToFile(data, "create_vnet.sh")
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
