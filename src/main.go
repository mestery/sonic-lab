package main

import (
	"fmt"
	"os"
	"log"
)

func main() {
	var numSpine int
	var numLeaf int
    var numLink int

	file, err := os.Create("docker-compose.yml")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	fmt.Print("Enter the Number of Spines you want in your Lab: ")
    _, err = fmt.Scan(&numSpine)
	if err != nil {
        fmt.Println("Error reading input:", err)
        return
    }

	fmt.Print("Enter the Number of Leafs you want in your Lab: ")
    _, err = fmt.Scan(&numLeaf)
	if err != nil {
        fmt.Println("Error reading input:", err)
        return
    }

	fmt.Print("Enter the Number of Links you want in your Lab: ")
	_, err = fmt.Scan(&numLink)
	if err != nil {
        fmt.Println("Error reading input:", err)
        return
    }

	writeToFile(numSpine, numLeaf, numLink, file)
}

func writeToFile(numSpine int, numLeaf int, numLink int, file *os.File) {
	fileData := []byte(`version: "3.9"

networks:
`)

	networksData := networksGeneration(numSpine, numLeaf, numLink)
	fileData = append(fileData, networksData...)

	xSonicCommon := `x-sonic-common: &sonic-common
  image: docker-sonic-vs:latest
  platform: linux/amd64
  privileged: true
  tty: true
  shm_size: "2gb"
  security_opt:
    - seccomp=unconfined

`
	fileData = append(fileData, xSonicCommon...)

	servicesData := servicesGeneration(numSpine, numLeaf, numLink)
	fileData = append(fileData, servicesData...)

	topologyInit := `  topology-init:
    image: docker:26
    container_name: sonic-topology-init
    depends_on:
      - spine
      - leaf1
      - leaf2
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    entrypoint: ["/bin/sh", "-c"]
    command: >
      "echo 'Waiting for SONiC spine to be ready...';
      until docker exec sonic-spine sh -c 'sonic-cfggen -h >/dev/null 2>&1'; do
        echo '  spine not ready yet...';
        sleep 5;
      done;
      echo 'Running containers:';
      docker ps --filter 'name=sonic-';
      echo 'Sleep 30...';
      sleep 30;
      echo 'Configuring spine ports...';
      docker exec sonic-spine sh -c 'config interface startup Ethernet0';
      docker exec sonic-spine sh -c 'config interface startup Ethernet4';
      docker exec sonic-spine sh -c 'config interface startup Ethernet8';
      docker exec sonic-spine sh -c 'config interface startup Ethernet12';
      echo 'Configuring leaf1 ports...';
      docker exec sonic-leaf1 sh -c 'config interface startup Ethernet0';
      docker exec sonic-leaf1 sh -c 'config interface startup Ethernet4';
      echo 'Configuring leaf2 ports...';
      docker exec sonic-leaf2 sh -c 'config interface startup Ethernet0';
      docker exec sonic-leaf2 sh -c 'config interface startup Ethernet4';
      echo 'Topology configured.'"
`
	fileData = append(fileData, topologyInit...)

	// 0644 sets the file permissions (read/write for owner, read for others)
	err := os.WriteFile("docker-compose.yml", fileData, 0644) 
	if err != nil {
		log.Fatal(err)
	}
}

func networksGeneration(numSpine int, numLeaf int, numLink int) string {
	var gen string

    for a := 0; a <= numSpine - 1; a++ {
		for b := 1; b <= numLeaf; b++ {
			for c := 1; c <= numLink; c++ {
				spineStr := fmt.Sprintf("spine%d", a)
                if a == 0 {
                    spineStr = "spine"
                }
                gen += fmt.Sprintf("  %s-leaf%d-link%d:\n    driver: bridge\n    internal: true\n\n", spineStr, b, c)
			}
		}
	}

	return gen
}

func servicesGeneration(numSpine int, numLeaf int, numLink int) string {
	var gen string = "services:\n"

	gen += "  spine:\n    <<: *sonic-common\n    container_name: sonic-spine\n    hostname: spine\n    networks:\n"
    for leaf := 1; leaf <= numLeaf; leaf++ {
        for link := 1; link <= numLink; link++ {
            gen += fmt.Sprintf("      spine-leaf%d-link%d:\n", leaf, link)
        }
    }
    gen += "\n"

    for leaf := 1; leaf <= numLeaf; leaf++ {
        gen += fmt.Sprintf("  leaf%d:\n    <<: *sonic-common\n    container_name: sonic-leaf%d\n    hostname: leaf%d\n    networks:\n", leaf, leaf, leaf)
        for link := 1; link <= numLink; link++ {
            gen += fmt.Sprintf("      spine-leaf%d-link%d:\n", leaf, link)
        }
        gen += "\n"
    }

	return gen
}