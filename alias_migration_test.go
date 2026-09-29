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

package cobra

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const aliasMigrationNotice = "please use"

func newAliasMigrationRoot(migration map[string]string) (*Command, *Command) {
	rootCmd := &Command{Use: "root", AliasMigration: migration}
	projectCmd := &Command{Use: "project"}
	pullCmd := &Command{Use: "pull"}
	projectCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(projectCmd)
	return rootCmd, pullCmd
}

func executeAliasMigration(root *Command, args ...string) (string, error) {
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestAliasMigrationForwardsArgs(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync": "project pull",
	})
	pullCmd.Flags().String("message", "", "message")

	var gotArgs []string
	var gotMessage string
	pullCmd.Run = func(cmd *Command, args []string) {
		gotArgs = args
		gotMessage, _ = cmd.Flags().GetString("message")
	}

	output, err := executeAliasMigration(rootCmd,
		"old", "sync", "--message", "hello world", "my file", "--", "--after-dash",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMessage != "hello world" {
		t.Errorf("expected flag value %q, got %q", "hello world", gotMessage)
	}
	wantArgs := []string{"my file", "--after-dash"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("expected target args %q, got %q", wantArgs, gotArgs)
	}
	if count := strings.Count(output, aliasMigrationNotice); count != 1 {
		t.Errorf("expected exactly one migration notice, got %d in %q", count, output)
	}
	if !strings.Contains(output, `"old sync"`) || !strings.Contains(output, `"project pull"`) {
		t.Errorf("notice should mention old and new path, got %q", output)
	}
}

func TestAliasMigrationForwardsArgsVerbatimWithDisabledParsing(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync": "project pull",
	})
	pullCmd.DisableFlagParsing = true

	var gotArgs []string
	pullCmd.Run = func(_ *Command, args []string) {
		gotArgs = args
	}

	_, err := executeAliasMigration(rootCmd,
		"old", "sync", "--message", "hello world", "my file", "--", "--after-dash",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantArgs := []string{"--message", "hello world", "my file", "--", "--after-dash"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("expected target args %q, got %q", wantArgs, gotArgs)
	}
}

func TestAliasMigrationRealCommandCollision(t *testing.T) {
	rootCmd, _ := newAliasMigrationRoot(map[string]string{
		"sync": "project pull",
	})

	var ranRealCommand bool
	var gotArgs []string
	rootCmd.AddCommand(&Command{
		Use:                "sync",
		DisableFlagParsing: true,
		Run: func(_ *Command, args []string) {
			ranRealCommand = true
			gotArgs = args
		},
	})

	output, err := executeAliasMigration(rootCmd, "sync", "my file", "--", "--after-dash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ranRealCommand {
		t.Fatal("expected the real subcommand to run")
	}
	wantArgs := []string{"my file", "--", "--after-dash"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("expected real command args %q, got %q", wantArgs, gotArgs)
	}
	if strings.Contains(output, aliasMigrationNotice) {
		t.Errorf("expected no migration notice, got %q", output)
	}
}

func TestAliasMigrationChain(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync":    "legacy sync",
		"legacy sync": "project pull",
	})
	pullCmd.DisableFlagParsing = true

	var gotArgs []string
	pullCmd.Run = func(_ *Command, args []string) { gotArgs = args }

	output, err := executeAliasMigration(rootCmd, "old", "sync", "my file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantArgs := []string{"my file"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("expected target args %q, got %q", wantArgs, gotArgs)
	}
	if count := strings.Count(output, aliasMigrationNotice); count != 1 {
		t.Errorf("expected exactly one migration notice, got %d in %q", count, output)
	}
	if !strings.Contains(output, `"old sync"`) || !strings.Contains(output, `"project pull"`) {
		t.Errorf("notice should point at original and final path, got %q", output)
	}
}

func TestAliasMigrationCycle(t *testing.T) {
	rootCmd, _ := newAliasMigrationRoot(map[string]string{
		"old sync":  "new sync",
		"new sync":  "next sync",
		"next sync": "old sync",
	})

	var ran bool
	rootCmd.AddCommand(&Command{
		Use: "project",
		Run: func(_ *Command, _ []string) { ran = true },
	})

	output, err := executeAliasMigration(rootCmd, "old", "sync", "my file")
	if err == nil {
		t.Fatal("expected an error for a migration cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got %v", err)
	}
	if ran {
		t.Fatal("expected no command to run on a migration cycle")
	}
	if strings.Contains(output, aliasMigrationNotice) {
		t.Errorf("expected no migration notice on cycle, got %q", output)
	}
}

func TestAliasMigrationBadTarget(t *testing.T) {
	rootCmd, _ := newAliasMigrationRoot(map[string]string{
		"old sync": "ghost cmd",
	})

	var ran bool
	rootCmd.AddCommand(&Command{
		Use: "project",
		Run: func(_ *Command, _ []string) { ran = true },
	})

	output, err := executeAliasMigration(rootCmd, "old", "sync")
	if err == nil {
		t.Fatal("expected an error for an unknown migration target")
	}
	if ran {
		t.Fatal("expected no command to run for a bad migration target")
	}
	if strings.Contains(output, aliasMigrationNotice) {
		t.Errorf("expected no migration notice for a bad target, got %q", output)
	}
}

func TestAliasMigrationHelpFlag(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync": "project pull",
	})
	pullCmd.Run = emptyRun

	output, err := executeAliasMigration(rootCmd, "old", "sync", "--help")
	if err != nil {
		t.Fatalf("expected no error with --help, got %v", err)
	}
	if count := strings.Count(output, aliasMigrationNotice); count != 1 {
		t.Errorf("expected exactly one migration notice with --help, got %d in %q", count, output)
	}
	if !strings.Contains(output, "root project pull") {
		t.Errorf("expected help of the target command, got %q", output)
	}
}

func TestAliasMigrationHelpCommand(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync": "project pull",
	})
	pullCmd.Run = emptyRun

	// The built-in help command takes precedence; only a real invocation of
	// the migrated path should redirect and print a notice.
	output, err := executeAliasMigration(rootCmd, "help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(output, aliasMigrationNotice) {
		t.Errorf("expected no migration notice for plain help, got %q", output)
	}
}

func TestAliasMigrationInterleavedFlags(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync": "project pull",
	})
	rootCmd.PersistentFlags().String("root-flag", "", "root flag")
	pullCmd.DisableFlagParsing = true

	var gotArgs []string
	pullCmd.Run = func(_ *Command, args []string) { gotArgs = args }

	_, err := executeAliasMigration(rootCmd, "old", "--root-flag", "rv", "sync", "my file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantArgs := []string{"--root-flag", "rv", "my file"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("expected target args %q, got %q", wantArgs, gotArgs)
	}
}

func TestAliasMigrationWithTraverseChildren(t *testing.T) {
	rootCmd, pullCmd := newAliasMigrationRoot(map[string]string{
		"old sync": "project pull",
	})
	rootCmd.TraverseChildren = true
	pullCmd.DisableFlagParsing = true

	var gotArgs []string
	pullCmd.Run = func(_ *Command, args []string) { gotArgs = args }

	output, err := executeAliasMigration(rootCmd, "old", "sync", "my file", "--", "--after-dash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantArgs := []string{"my file", "--", "--after-dash"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("expected target args %q, got %q", wantArgs, gotArgs)
	}
	if count := strings.Count(output, aliasMigrationNotice); count != 1 {
		t.Errorf("expected exactly one migration notice, got %d in %q", count, output)
	}
}
