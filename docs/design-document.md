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
Example YML file given as an arg (samples provided):

topology:
  name: spine-leaf-lab

  switches:
    spine1:
      role: spine
      asn: 65000
      ports:
        Ethernet0: {}
        Ethernet4: {}
        Ethernet8: {}
        Ethernet12: {}

    leaf1:
      role: leaf
      asn: 65101
      ports:
        Ethernet0: {}
        Ethernet4: {}

    leaf2:
      role: leaf
      asn: 65102
      ports:
        Ethernet0: {}
        Ethernet4: {}

  links:
    - endpoints:
        - device: spine1
          port: Ethernet0
        - device: leaf1
          port: Ethernet0

    - endpoints:
        - device: spine1
          port: Ethernet4
        - device: leaf1
          port: Ethernet4

    - endpoints:
        - device: spine1
          port: Ethernet8
        - device: leaf2
          port: Ethernet0

    - endpoints:
        - device: spine1
          port: Ethernet12
        - device: leaf2
          port: Ethernet4