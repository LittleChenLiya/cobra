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
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCompletionViewSeesParsedArgsAndFlags verifies that the read-only view
// handed to a ValidArgsViewFunction exposes the parsed arguments and the
// effective (temporary) flags of the completion command-line.
func TestCompletionViewSeesParsedArgsAndFlags(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use: "sub",
		Run: emptyRun,
	}
	subCmd.Flags().String("name", "default", "a name")

	var viewArgs []string
	var viewToComplete, viewFlagValue, viewPath string
	var viewFlagChanged bool
	subCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
		viewArgs = view.Args()
		viewToComplete = view.ToComplete()
		viewFlagValue = view.Flag("name")
		viewFlagChanged = view.FlagChanged("name")
		viewPath = view.CommandPath()
		return []Completion{"choice"}, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(subCmd)

	output, err := executeCommand(rootCmd, ShellCompRequestCmd, "sub", "--name", "temp", "first", "sec")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "choice") {
		t.Errorf("expected completion candidate %q in output: %q", "choice", output)
	}
	if got, want := strings.Join(viewArgs, ","), "first"; got != want {
		t.Errorf("expected view args %q, got %q", want, got)
	}
	if viewToComplete != "sec" {
		t.Errorf("expected toComplete %q, got %q", "sec", viewToComplete)
	}
	if viewFlagValue != "temp" {
		t.Errorf("expected view flag value %q, got %q", "temp", viewFlagValue)
	}
	if !viewFlagChanged {
		t.Error("expected view to report the temporary flag as changed")
	}
	if viewPath != "root sub" {
		t.Errorf("expected command path %q, got %q", "root sub", viewPath)
	}
}

// TestCompletionViewDoesNotPolluteExecute verifies that temporary flags used
// while completing are rolled back: a normal Execute afterwards still sees the
// registered default values and no leftover Changed state.
func TestCompletionViewDoesNotPolluteExecute(t *testing.T) {
	var executedName string
	var executedChanged bool
	businessRan := false

	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use: "sub",
		Run: func(cmd *Command, args []string) {
			businessRan = true
			executedName, _ = cmd.Flags().GetString("name")
			executedChanged = cmd.Flags().Changed("name")
		},
	}
	subCmd.Flags().String("name", "default", "a name")
	subCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
		return []Completion{"choice"}, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(subCmd)

	// Complete with a temporary flag value.
	if _, err := executeCommand(rootCmd, ShellCompRequestCmd, "sub", "--name", "temp", ""); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if businessRan {
		t.Error("the business Run function must not be executed during completion")
	}

	// The flag must have been rolled back to its registered baseline.
	flag := subCmd.Flags().Lookup("name")
	if flag.Value.String() != "default" {
		t.Errorf("expected flag value rolled back to %q, got %q", "default", flag.Value.String())
	}
	if flag.Changed {
		t.Error("expected flag Changed state to be rolled back after completion")
	}

	// A normal Execute must still see the original default value.
	if _, err := executeCommand(rootCmd, "sub"); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !businessRan {
		t.Error("expected the business Run function to execute")
	}
	if executedName != "default" {
		t.Errorf("expected default flag value %q during Execute, got %q", "default", executedName)
	}
	if executedChanged {
		t.Error("expected flag to be unchanged during Execute after completion")
	}
}

// TestCompletionViewRestoresBaselineOnError verifies that the flag baseline is
// restored even when completion ends with an error.
func TestCompletionViewRestoresBaselineOnError(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use: "sub",
		Run: emptyRun,
	}
	subCmd.Flags().String("name", "default", "a name")
	subCmd.ValidArgsFunction = func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return nil, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(subCmd)

	// Complete with an unknown flag: completion ends with an error (reported
	// on stderr) but must still roll back the flag baseline.
	if _, err := executeCommand(rootCmd, ShellCompRequestCmd, "sub", "--name", "temp", "--bogus", ""); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	flag := subCmd.Flags().Lookup("name")
	if flag.Value.String() != "default" {
		t.Errorf("expected flag value rolled back to %q after error, got %q", "default", flag.Value.String())
	}
	if flag.Changed {
		t.Error("expected flag Changed state to be rolled back after error")
	}
}

// TestCompletionViewRejectsMutation verifies that attempts to modify the
// command tree through the view are explicitly rejected.
func TestCompletionViewRejectsMutation(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use: "sub",
		Run: emptyRun,
	}
	subCmd.Flags().String("name", "default", "a name")

	var setErr, addErr, execErr error
	subCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
		setErr = view.SetFlag("name", "hacked")
		addErr = view.AddCommand(&Command{Use: "sneaky"})
		execErr = view.Execute()
		return []Completion{"choice"}, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(subCmd)

	if _, err := executeCommand(rootCmd, ShellCompRequestCmd, "sub", ""); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	for name, err := range map[string]error{"SetFlag": setErr, "AddCommand": addErr, "Execute": execErr} {
		if !errors.Is(err, ErrCompletionViewReadOnly) {
			t.Errorf("expected %s to fail with ErrCompletionViewReadOnly, got %v", name, err)
		}
	}

	// The command tree and the flag must be untouched.
	if cmd, _, err := rootCmd.Find([]string{"sneaky"}); err == nil && cmd.Name() == "sneaky" {
		t.Error("the view must not be able to add commands to the tree")
	}
	if got := subCmd.Flags().Lookup("name").Value.String(); got != "default" {
		t.Errorf("expected flag value %q, got %q", "default", got)
	}
}

// TestCompletionCancellationStopsPublication verifies that a cancelled context
// prevents completion candidates from being published.
func TestCompletionCancellationStopsPublication(t *testing.T) {
	buildRoot := func() *Command {
		rootCmd := &Command{Use: "root"}
		subCmd := &Command{
			Use: "sub",
			Run: emptyRun,
		}
		subCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
			return []Completion{"late-candidate"}, ShellCompDirectiveNoFileComp
		}
		rootCmd.AddCommand(subCmd)
		return rootCmd
	}

	// First, a sanity check with a live context: the candidate is published.
	output, err := executeCommandWithContext(context.Background(), buildRoot(), ShellCompRequestCmd, "sub", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "late-candidate") {
		t.Fatalf("expected candidate to be published with a live context: %q", output)
	}

	// With a cancelled context, the late candidate must not appear, but the
	// directive marker must still be printed for the completion script.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output, err = executeCommandWithContext(ctx, buildRoot(), ShellCompRequestCmd, "sub", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if strings.Contains(output, "late-candidate") {
		t.Errorf("expected no candidates after cancellation, got: %q", output)
	}
	if !strings.Contains(output, ":") {
		t.Errorf("expected the directive marker to still be printed, got: %q", output)
	}
}

// TestCompletionViewThroughAliases verifies that both command aliases and the
// __completeNoDesc alias of the completion request command go through the same
// read-only view.
func TestCompletionViewThroughAliases(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use:     "deploy",
		Aliases: []string{"dep"},
		Run:     emptyRun,
	}
	viewCalls := 0
	subCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
		viewCalls++
		return []Completion{CompletionWithDesc("aliased-choice", "a choice")}, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(subCmd)

	// Completing through the command alias must use the same view.
	output, err := executeCommand(rootCmd, ShellCompRequestCmd, "dep", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "aliased-choice\ta choice") {
		t.Errorf("expected candidate with description through alias, got: %q", output)
	}

	// The __completeNoDesc alias of the request command must use the same
	// view, only stripping the descriptions.
	output, err = executeCommand(rootCmd, ShellCompNoDescRequestCmd, "dep", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "aliased-choice\n") {
		t.Errorf("expected candidate through __completeNoDesc, got: %q", output)
	}
	if strings.Contains(output, "a choice") {
		t.Errorf("expected descriptions to be stripped by __completeNoDesc, got: %q", output)
	}

	if viewCalls != 2 {
		t.Errorf("expected the view function to be called twice, got %d", viewCalls)
	}
}

// TestFlagCompletionViewFunc verifies flag value completion through a
// registered CompletionViewFunc, which sees the other effective flags, and
// that the legacy registration entry points remain compatible.
func TestFlagCompletionViewFunc(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use: "sub",
		Run: emptyRun,
	}
	subCmd.Flags().String("env", "dev", "target environment")
	subCmd.Flags().String("region", "", "target region")
	rootCmd.AddCommand(subCmd)

	var seenEnv string
	err := subCmd.RegisterFlagCompletionViewFunc("region", func(view *CompletionView) ([]Completion, ShellCompDirective) {
		seenEnv = view.Flag("env")
		return []Completion{"us-east-1"}, ShellCompDirectiveNoFileComp
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if err := subCmd.RegisterFlagCompletionViewFunc("region", nil); err == nil {
		t.Error("expected an error when registering a flag view completion function twice")
	}
	if err := subCmd.RegisterFlagCompletionViewFunc("unknown", nil); err == nil {
		t.Error("expected an error when registering for an unknown flag")
	}
	if _, ok := subCmd.GetFlagCompletionViewFunc("region"); !ok {
		t.Error("expected GetFlagCompletionViewFunc to find the registered function")
	}
	if _, ok := subCmd.GetFlagCompletionViewFunc("env"); ok {
		t.Error("expected GetFlagCompletionViewFunc to not find a function for env")
	}

	output, err := executeCommand(rootCmd, ShellCompRequestCmd, "sub", "--env", "prod", "--region", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "us-east-1") {
		t.Errorf("expected flag completion candidate in output: %q", output)
	}
	if seenEnv != "prod" {
		t.Errorf("expected the view to see --env=%q, got %q", "prod", seenEnv)
	}

	// The temporary --env flag must have been rolled back.
	envFlag := subCmd.Flags().Lookup("env")
	if envFlag.Value.String() != "dev" || envFlag.Changed {
		t.Errorf("expected --env rolled back to its baseline, got value=%q changed=%v", envFlag.Value.String(), envFlag.Changed)
	}
}

// TestCompletionViewPrecedence verifies that a view function takes precedence
// over the legacy compatible callback when both are registered.
func TestCompletionViewPrecedence(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	subCmd := &Command{
		Use: "sub",
		Run: emptyRun,
	}
	legacyCalled := false
	subCmd.ValidArgsFunction = func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		legacyCalled = true
		return []Completion{"legacy"}, ShellCompDirectiveNoFileComp
	}
	subCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
		return []Completion{"view"}, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(subCmd)

	output, err := executeCommand(rootCmd, ShellCompRequestCmd, "sub", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "view") {
		t.Errorf("expected the view function candidate in output: %q", output)
	}
	if legacyCalled {
		t.Error("expected the legacy callback to not be called when a view function is set")
	}
}

// ExampleCompletionViewFunc demonstrates dynamic completion through the
// read-only CompletionView: the candidates depend on the effective value of a
// temporary flag, without ever modifying the command tree.
func ExampleCompletionViewFunc() {
	rootCmd := &Command{Use: "proj"}
	deployCmd := &Command{
		Use: "deploy",
		Run: func(cmd *Command, args []string) {},
	}
	deployCmd.Flags().String("env", "dev", "target environment")
	deployCmd.ValidArgsViewFunction = func(view *CompletionView) ([]Completion, ShellCompDirective) {
		// The view exposes the parsed flags of the completion command-line.
		if view.Flag("env") == "prod" {
			return []Completion{"us-east-1", "eu-west-1"}, ShellCompDirectiveNoFileComp
		}
		return []Completion{"dev-local"}, ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(deployCmd)

	rootCmd.SetArgs([]string{ShellCompRequestCmd, "deploy", "--env", "prod", ""})
	_ = rootCmd.Execute()

	// Output:
	// us-east-1
	// eu-west-1
	// :4
}
