# Design Document - GoLang Generation Script

## Overview
The lab must be started in three phases:

1. Start base Linux containers
2. Create the veth topology
3. Start SONiC containers

## Updates
1. Generates scripts if necessary:
    - Generates init_linux.sh if not already made.
    - Generates sonic_entry.sh if not already made.
    - Generates start.sh if not already made.

2. **[Topology Info as Input](#Collecting-Topologogy)**
    - Generate create_vnet.sh
    - GoLang script takes in user input for topology (yml file)

~~3. Program runs with command line flags:
    - "s" for # of spines
    - "l" for # of leafs
    - "k" for # of links (between)~~

3. Replace command line flags with yml file

4. Update parse GoLang script to generate accordingly
    - Backing Linux containers
    - SONiC VS containers

5. Start the base Linux Containers
    - docker compose up -d spine1-linux ...

6. Create veth topology
    - colima ssh sudo ./create_vnet.ssh

7. Start SONiC Containers
    - docker compose up -d

## Collecting Topologogy Layout
Example Input from User:

SPINE, LEAF1, LEAF2
SPINE-LEAF1 with eth1 eth1
SPINE-LEAF1 with eth2 eth2
SPINE-LEAF2 with eth3 eth1
SPINE-LEAF2 with eth4 eth2