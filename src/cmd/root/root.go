/*
Copyright © 2026 Tyler Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package cmd

import (
	"fmt"
	"os"
	"github.com/spf13/cobra"
)

var leaf int
var spine int
var link int

var rootCmd = &cobra.Command{
	Use:   "sonic-lab",
	Short: "Generate a SONiC lab",
	RunE: func(cmd *cobra.Command, args []string) error {
		if leaf <= 0 || spine <= 0 || link <= 0 {
			return fmt.Errorf("-l, -s, and -k must be > 0")
		}

		fmt.Println("leaf:", leaf, "spine:", spine, "link:", link)
		return nil
	},
}

func init() {
	rootCmd.Flags().IntVarP(&leaf, "leaf", "l", 0, "number of leafs")
	rootCmd.Flags().IntVarP(&spine, "spine", "s", 0, "number of spines")
	rootCmd.Flags().IntVarP(&link, "link", "k", 0, "number of links")

	// Required:
	rootCmd.MarkFlagRequired("leaf")
	rootCmd.MarkFlagRequired("spine")
	rootCmd.MarkFlagRequired("link")
}

func Execute() (int, int, int) {
    if err := rootCmd.Execute(); err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
    return spine, leaf, link
}