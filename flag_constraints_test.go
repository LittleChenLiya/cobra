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
	"strings"
	"testing"
)

// remoteTestCmd builds a command where "remote" implies "endpoint" and
// "remote" is mutually exclusive with "file".
func remoteTestCmd(run func(cmd *Command, args []string)) *Command {
	c := &Command{Use: "sync", Run: run}
	c.Flags().Bool("remote", false, "use remote mode")
	c.Flags().String("endpoint", "", "remote endpoint address")
	c.Flags().String("file", "", "local file")
	if err := c.AddFlagConstraint("remote-requires-endpoint", Implies(Present("remote"), Present("endpoint"))); err != nil {
		panic(err)
	}
	if err := c.AddFlagConstraint("remote-file-exclusive", Not(All(Present("remote"), Present("file")))); err != nil {
		panic(err)
	}
	return c
}

func TestFlagConstraintsRemoteEndpoint(t *testing.T) {
	ran := false
	c := remoteTestCmd(func(cmd *Command, args []string) { ran = true })

	// Passing only --remote must report the missing endpoint.
	_, err := executeCommand(c, "--remote")
	if err == nil {
		t.Fatal("expected an error when --remote is given without --endpoint")
	}
	if !strings.Contains(err.Error(), "remote-requires-endpoint") {
		t.Errorf("expected error to mention the rule ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("expected error to mention the missing flag, got: %v", err)
	}
	if ran {
		t.Error("command must not run when a constraint is violated")
	}

	// Passing both --remote and --file must report the conflict.
	_, err = executeCommand(remoteTestCmd(func(cmd *Command, args []string) {}), "--remote", "--endpoint", "https://x", "--file", "f.txt")
	if err == nil || !strings.Contains(err.Error(), "remote-file-exclusive") {
		t.Errorf("expected remote-file-exclusive violation, got: %v", err)
	}

	// Passing --remote with --endpoint is valid.
	ran = false
	_, err = executeCommand(remoteTestCmd(func(cmd *Command, args []string) { ran = true }), "--remote", "--endpoint", "https://x")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !ran {
		t.Error("expected command to run")
	}

	// No flags at all is valid too.
	if _, err = executeCommand(remoteTestCmd(func(cmd *Command, args []string) {})); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFlagConstraintsCompletion(t *testing.T) {
	c := remoteTestCmd(func(cmd *Command, args []string) {
		t.Error("Run must not be executed during completion")
	})

	output, err := executeCommand(c, ShellCompRequestCmd, "--remote", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// --endpoint can still lead to a valid combination and must be suggested.
	if !strings.Contains(output, "--endpoint") {
		t.Errorf("expected completion to suggest --endpoint, got: %q", output)
	}
	// --file can never lead to a valid combination once --remote is set.
	if strings.Contains(output, "--file") {
		t.Errorf("expected completion to filter out --file, got: %q", output)
	}
	// Unrelated flags keep being suggested.
	if !strings.Contains(output, "--help") {
		t.Errorf("expected completion to still suggest --help, got: %q", output)
	}

	// Without --remote, all flags remain candidates.
	output, err = executeCommand(remoteTestCmd(func(cmd *Command, args []string) {}), ShellCompRequestCmd, "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, candidate := range []string{"--remote", "--endpoint", "--file"} {
		if !strings.Contains(output, candidate) {
			t.Errorf("expected completion to suggest %s, got: %q", candidate, output)
		}
	}
}

func TestFlagConstraintsExplicitFalseCountsAsPresent(t *testing.T) {
	getCmd := func() *Command {
		c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
		c.Flags().Bool("enabled", false, "enable the feature")
		c.Flags().String("config", "", "config file")
		if err := c.AddFlagConstraint("enabled-needs-config", Implies(Present("enabled"), Present("config"))); err != nil {
			panic(err)
		}
		return c
	}

	// An explicit --enabled=false still counts as present.
	_, err := executeCommand(getCmd(), "--enabled=false")
	if err == nil || !strings.Contains(err.Error(), "enabled-needs-config") {
		t.Errorf("expected explicit false to count as present, got: %v", err)
	}

	// A default value does not count as present.
	if _, err := executeCommand(getCmd()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFlagConstraintsShadowedFlagBinding(t *testing.T) {
	root := &Command{Use: "root", Run: func(cmd *Command, args []string) {}}
	root.PersistentFlags().Bool("mode", false, "root persistent mode")

	child := &Command{Use: "child", Run: func(cmd *Command, args []string) {}}
	child.Flags().Bool("mode", false, "child local mode shadows the persistent one")
	child.Flags().String("other", "", "other flag")
	if err := child.AddFlagConstraint("mode-needs-other", Implies(Present("mode"), Present("other"))); err != nil {
		panic(err)
	}
	root.AddCommand(child)

	// The constraint must bind to the child's local "mode" flag.
	_, err := executeCommand(root, "child", "--mode")
	if err == nil || !strings.Contains(err.Error(), "mode-needs-other") || !strings.Contains(err.Error(), "other") {
		t.Errorf("expected violation bound to the local flag, got: %v", err)
	}
	if root.PersistentFlags().Lookup("mode").Changed {
		t.Error("the shadowed persistent flag must not be marked as changed")
	}

	// Providing both flags satisfies the constraint.
	if _, err = executeCommand(root, "child", "--mode", "--other", "x"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// A constraint on the parent referencing an inherited persistent flag
	// binds to that flag when the child executes.
	root2 := &Command{Use: "root", Run: func(cmd *Command, args []string) {}}
	root2.PersistentFlags().Bool("verbose", false, "inherited verbose")
	child2 := &Command{Use: "child", Run: func(cmd *Command, args []string) {}}
	child2.Flags().String("config", "", "config file")
	if err := root2.AddFlagConstraint("verbose-needs-config", Implies(Present("verbose"), Present("config"))); err != nil {
		panic(err)
	}
	root2.AddCommand(child2)
	if _, err = executeCommand(root2, "child", "--verbose"); err == nil || !strings.Contains(err.Error(), "verbose-needs-config") {
		t.Errorf("expected violation for inherited persistent flag, got: %v", err)
	}
}

func TestFlagConstraintsUnsatisfiableCombination(t *testing.T) {
	getCmd := func() *Command {
		c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
		c.Flags().Bool("alpha", false, "alpha flag")
		c.Flags().Bool("beta", false, "beta flag")
		// The two rules can never be satisfied at the same time.
		if err := c.AddFlagConstraint("alpha-implies-beta", Implies(Present("alpha"), Present("beta"))); err != nil {
			panic(err)
		}
		if err := c.AddFlagConstraint("alpha-without-beta", All(Present("alpha"), Not(Present("beta")))); err != nil {
			panic(err)
		}
		return c
	}

	// Execution always fails; with no flags the second rule is violated.
	_, err := executeCommand(getCmd())
	if err == nil || !strings.Contains(err.Error(), "alpha-without-beta") {
		t.Errorf("expected alpha-without-beta violation, got: %v", err)
	}

	// Completion must filter both flags: neither can be part of a valid
	// combination, but unrelated candidates remain.
	output, err := executeCommand(getCmd(), ShellCompRequestCmd, "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(output, "--alpha") || strings.Contains(output, "--beta") {
		t.Errorf("expected unsatisfiable flags to be filtered, got: %q", output)
	}
	if !strings.Contains(output, "--help") {
		t.Errorf("expected --help to remain, got: %q", output)
	}
}

func TestFlagConstraintsSharedFlagAndViolationOrder(t *testing.T) {
	c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
	c.Flags().Bool("shared", false, "shared flag")
	c.Flags().String("x", "", "x flag")
	c.Flags().String("y", "", "y flag")
	if err := c.AddFlagConstraint("first-rule", Implies(Present("shared"), Present("x"))); err != nil {
		panic(err)
	}
	if err := c.AddFlagConstraint("second-rule", Implies(Present("shared"), Present("y"))); err != nil {
		panic(err)
	}

	_, err := executeCommand(c, "--shared")
	if err == nil {
		t.Fatal("expected an error")
	}
	// Both violations are reported in rule registration order.
	first := strings.Index(err.Error(), "first-rule")
	second := strings.Index(err.Error(), "second-rule")
	if first == -1 || second == -1 || first > second {
		t.Errorf("expected both rule IDs in registration order, got: %v", err)
	}

	// Completion after --shared suggests both missing flags.
	output, err := executeCommand(c, ShellCompRequestCmd, "--shared", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "--x") || !strings.Contains(output, "--y") {
		t.Errorf("expected --x and --y to be suggested, got: %q", output)
	}
}

func TestFlagConstraintsCompletionSkipsCallbacks(t *testing.T) {
	c := remoteTestCmd(func(cmd *Command, args []string) {
		t.Error("Run must not be executed during completion")
	})
	c.PreRun = func(cmd *Command, args []string) {
		t.Error("PreRun must not be executed during completion")
	}
	c.ValidArgsFunction = func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		t.Error("ValidArgsFunction must not be executed for flag name completion")
		return nil, ShellCompDirectiveNoFileComp
	}
	if _, err := executeCommand(c, ShellCompRequestCmd, "--remote", "-"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlagConstraintsRuleRef(t *testing.T) {
	getCmd := func() *Command {
		c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
		c.Flags().Bool("remote", false, "remote mode")
		c.Flags().String("endpoint", "", "endpoint")
		if err := c.AddFlagConstraint("base", Implies(Present("remote"), Present("endpoint"))); err != nil {
			panic(err)
		}
		if err := c.AddFlagConstraint("derived", RuleRef("base")); err != nil {
			panic(err)
		}
		return c
	}

	_, err := executeCommand(getCmd(), "--remote")
	if err == nil {
		t.Fatal("expected an error")
	}
	// Both the base rule and the referencing rule report the violation.
	if !strings.Contains(err.Error(), "base") || !strings.Contains(err.Error(), "derived") {
		t.Errorf("expected both rule IDs, got: %v", err)
	}
	if _, err = executeCommand(getCmd(), "--remote", "--endpoint", "e"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFlagConstraintsRegistrationErrors(t *testing.T) {
	newExpr := func() *ConstraintExpr { return Present("a") }
	oversize := Present("a")
	for i := 0; i < MaxFlagConstraintSize; i++ {
		oversize = Not(oversize)
	}

	testcases := []struct {
		desc   string
		ruleID string
		expr   *ConstraintExpr
		expect string
	}{
		{desc: "empty rule ID", ruleID: "", expr: newExpr(), expect: "must not be empty"},
		{desc: "nil expression", ruleID: "r", expr: nil, expect: "must not be nil"},
		{desc: "empty All", ruleID: "r", expr: All(), expect: "All expression without sub-expressions"},
		{desc: "empty Any", ruleID: "r", expr: Any(), expect: "Any expression without sub-expressions"},
		{desc: "empty flag name", ruleID: "r", expr: Present(""), expect: "empty flag name"},
		{desc: "empty ref", ruleID: "r", expr: RuleRef(""), expect: "empty rule ID"},
		{desc: "nil child", ruleID: "r", expr: All(newExpr(), nil), expect: "nil sub-expression"},
		{desc: "oversize", ruleID: "r", expr: oversize, expect: "maximum expression size"},
	}
	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
			c.Flags().Bool("a", false, "")
			err := c.AddFlagConstraint(tc.ruleID, tc.expr)
			if err == nil || !strings.Contains(err.Error(), tc.expect) {
				t.Errorf("expected error containing %q, got: %v", tc.expect, err)
			}
		})
	}

	// Duplicate rule IDs are rejected.
	c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
	c.Flags().Bool("a", false, "")
	if err := c.AddFlagConstraint("dup", Present("a")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.AddFlagConstraint("dup", Present("a")); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("expected duplicate registration error, got: %v", err)
	}
}

func TestFlagConstraintsResolutionErrors(t *testing.T) {
	// Unknown flag.
	c := &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
	c.Flags().Bool("a", false, "")
	if err := c.AddFlagConstraint("bad-flag", Present("nope")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := executeCommand(c); err == nil || !strings.Contains(err.Error(), "unknown flag \"nope\"") {
		t.Errorf("expected unknown flag error, got: %v", err)
	}

	// Unknown reference.
	c = &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
	c.Flags().Bool("a", false, "")
	if err := c.AddFlagConstraint("bad-ref", RuleRef("missing")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := executeCommand(c); err == nil || !strings.Contains(err.Error(), "unknown constraint rule \"missing\"") {
		t.Errorf("expected unknown reference error, got: %v", err)
	}

	// Reference cycle across two rules.
	c = &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
	c.Flags().Bool("a", false, "")
	if err := c.AddFlagConstraint("cycle-a", RuleRef("cycle-b")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.AddFlagConstraint("cycle-b", RuleRef("cycle-a")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := executeCommand(c); err == nil || !strings.Contains(err.Error(), "circular reference") {
		t.Errorf("expected circular reference error, got: %v", err)
	}

	// Self reference.
	c = &Command{Use: "testcmd", Run: func(cmd *Command, args []string) {}}
	c.Flags().Bool("a", false, "")
	if err := c.AddFlagConstraint("self", RuleRef("self")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := executeCommand(c); err == nil || !strings.Contains(err.Error(), "circular reference") {
		t.Errorf("expected circular reference error, got: %v", err)
	}
}
