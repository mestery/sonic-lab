/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package internal

import (
	"os"

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

	return nil
}
