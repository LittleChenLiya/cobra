// Copyright 2013-2023 The Cobra Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main demonstrates dynamic shell completion with a read-only
// command view. The completion callbacks can inspect parsed args and valid
// flags, but cannot modify the command tree or leave Changed state that
// would pollute a subsequent Execute.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// newRootCmd creates the root command with dynamic completion registered.
// Each call returns a fresh command tree so that tests can run in isolation.
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "dynacomp",
		Short: "A dynamic completion example",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "Hello from dynacomp!")
		},
	}

	// Register a flag with a completion function. The callback receives
	// the parsed args and the current command, but operates on a read-only
	// view, so any changes to flags are discarded when completion ends.
	rootCmd.Flags().String("environment", "dev", "target environment")
	_ = rootCmd.RegisterFlagCompletionFunc("environment", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// The callback can inspect the parsed args and valid flags.
		env, _ := cmd.Flags().GetString("environment")
		fmt.Fprintf(os.Stderr, "completing environment (current: %s)\n", env)

		// Return dynamic candidates based on the current state.
		all := []string{"dev", "staging", "production"}
		var completions []string
		for _, c := range all {
			if strings.HasPrefix(c, toComplete) {
				completions = append(completions, c)
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	})

	// Register a ValidArgsFunction for positional arg completion.
	rootCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// The business Run is not executed during completion; only the
		// completion callback is invoked.
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return []string{"service", "database", "cache"}, cobra.ShellCompDirectiveNoFileComp
	}

	// Add a subcommand with its own completion.
	subCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a service",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "Deploying...")
		},
	}
	subCmd.Flags().String("region", "us-east-1", "AWS region")
	_ = subCmd.RegisterFlagCompletionFunc("region", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"us-east-1", "us-west-2", "eu-west-1"}, cobra.ShellCompDirectiveNoFileComp
	})
	rootCmd.AddCommand(subCmd)

	return rootCmd
}

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
