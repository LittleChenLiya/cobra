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

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDynamicCompletionEnvironmentFlag(t *testing.T) {
	// Test that the environment flag completion returns the expected candidates.
	output, err := executeCompletion("__complete", "--environment", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := strings.Join([]string{
		"dev",
		"staging",
		"production",
		":4",
		"Completion ended with directive: ShellCompDirectiveNoFileComp", "",
	}, "\n")

	if output != expected {
		t.Errorf("expected: %q, got: %q", expected, output)
	}
}

func TestDynamicCompletionEnvironmentFlagWithPrefix(t *testing.T) {
	// Test that the environment flag completion filters by prefix.
	output, err := executeCompletion("__complete", "--environment", "st")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := strings.Join([]string{
		"staging",
		":4",
		"Completion ended with directive: ShellCompDirectiveNoFileComp", "",
	}, "\n")

	if output != expected {
		t.Errorf("expected: %q, got: %q", expected, output)
	}
}

func TestDynamicCompletionPositionalArgs(t *testing.T) {
	// Test that positional arg completion returns the expected candidates.
	// When the root has subcommands, completion includes both subcommands
	// and positional args.
	output, err := executeCompletion("__complete", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// The output includes subcommands (completion, deploy, help) and
	// positional args (service, database, cache).
	if !strings.Contains(output, "service") {
		t.Errorf("expected output to contain 'service', got: %q", output)
	}
	if !strings.Contains(output, "database") {
		t.Errorf("expected output to contain 'database', got: %q", output)
	}
	if !strings.Contains(output, "cache") {
		t.Errorf("expected output to contain 'cache', got: %q", output)
	}
}

func TestDynamicCompletionSubcommand(t *testing.T) {
	// Test that the subcommand flag completion returns the expected candidates.
	output, err := executeCompletion("__complete", "deploy", "--region", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := strings.Join([]string{
		"us-east-1",
		"us-west-2",
		"eu-west-1",
		":4",
		"Completion ended with directive: ShellCompDirectiveNoFileComp", "",
	}, "\n")

	if output != expected {
		t.Errorf("expected: %q, got: %q", expected, output)
	}
}

func TestDynamicCompletionNoChangedPollution(t *testing.T) {
	// First, trigger completion which parses the temporary flag.
	_, err := executeCompletion("__complete", "--environment", "")
	if err != nil {
		t.Fatalf("Unexpected error during completion: %v", err)
	}

	// Then, execute the command normally and verify the flag is unchanged.
	output, err := executeNormal()
	if err != nil {
		t.Fatalf("Unexpected error during normal execution: %v", err)
	}

	expected := "Hello from dynacomp!\n"
	if output != expected {
		t.Errorf("expected: %q, got: %q", expected, output)
	}
}

// executeCompletion runs the completion command with the given args and
// returns the output.
func executeCompletion(args ...string) (string, error) {
	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	return buf.String(), err
}

// executeNormal runs the command normally and returns the output.
func executeNormal() (string, error) {
	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{})

	err := rootCmd.Execute()
	return buf.String(), err
}
