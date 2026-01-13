/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package main

import (
    root "sonic-lab/cmd/root"
    "sonic-lab/internal/parser"
    // "github.com/mestery/sonic-lab/cmds/root"
    // "github.com/mestery/sonic-lab/src/internal/parser"
)

func main() {
    spine, leaf, link := root.Execute()
    parser.Runner(spine, leaf, link)
}