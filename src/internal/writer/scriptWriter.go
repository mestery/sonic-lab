/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package internal

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
	files := []string{
		"create_vnet.sh",
		"start.sh",
		"sonic_entry.sh",
		"init_linux.sh",
		"docker-compose.yml",
	}

	if err := os.MkdirAll(w.OutputDir, 0o755); err != nil {
		return err
	}

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
