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

	"sonic-lab/internal/reader"
	"sonic-lab/internal/writer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: sonic-lab <.yml> [output-dir]")
		os.Exit(1)
	}

	ymlPath := os.Args[1]
	outputDir := "./scripts"
	if len(os.Args) >= 3 {
		outputDir = os.Args[2]
	}

	if err := reader.Run(ymlPath, outputDir); err != nil {
		log.Fatalf("Error reading config: %v", err)
	}

	if err := writer.New(outputDir).Run(); err != nil {
		log.Fatalf("Error writing scripts: %v", err)
	}

	fmt.Printf("Wrote topology files to %s\n", outputDir)
}
