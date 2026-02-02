/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package main

import (
	"fmt"
	"log"
	"os"
	reader "sonic-lab/internal/reader"
	internal "sonic-lab/internal/writer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go main.go <.yml>")
		os.Exit(1)
	}

	ymlPath := os.Args[1]
	if err := reader.Run(ymlPath); err != nil {
		fmt.Println("Error reading config:", err)
		os.Exit(1)
	}

	writer := internal.New("./scripts")
	if err := writer.Run(); err != nil {
		fmt.Println("Error writing scripts:", err)
		log.Fatal(err)
	}
}
