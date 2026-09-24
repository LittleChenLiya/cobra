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

// newRemoteCmd builds the acceptance-test command: remote implies endpoint,
// and remote is mutually exclusive with file.
func newRemoteCmd(runFunc func(*Command, []string)) *Command {
	c := &Command{
		Use: "sync",
		Run: runFunc,
	}
	c.Flags().Bool("remote", false, "use remote mode")
	c.Flags().String("endpoint", "", "remote endpoint address")
	c.Flags().String("file", "", "local file")
	if err := c.AddFlagConstraint("remote-requires-endpoint", Implies(Present("remote"), Present("endpoint"))); err != nil {
		panic(err)
	}
	if err := c.AddFlagConstraint("remote-excludes-file", Implies(Present("remote"), Not(Present("file")))); err != nil {
		panic(err)
	}
	return c
}

func TestFlagConstraintRemoteImpliesEndpoint(t *testing.T) {
	ran := false
	newRoot := func() *Command {
		root := &Command{Use: "root"}
		root.AddCommand(newRemoteCmd(func(*Command, []string) { ran = true }))
		return root
	}

	// remote alone must report the missing endpoint.
	_, err := executeCommand(newRoot(), "sync", "--remote")
	if err == nil {
		t.Fatal("expected an error when --remote is given without --endpoint")
	}
	if !strings.Contains(err.Error(), `"remote-requires-endpoint"`) {
		t.Errorf("error should mention the rule ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "endpoint") || !strings.Contains(err.Error(), "remote") {
		t.Errorf("error should mention the bound flag names, got: %v", err)
	}
	if ran {
		t.Error("Run must not be called when a flag constraint is violated")
	}

	// Adding the endpoint satisfies the constraints.
	ran = false
	if _, err := executeCommand(newRoot(), "sync", "--remote", "--endpoint", "https://example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ran {
		t.Error("Run should have been called once the constraints are satisfied")
	}

	// remote together with file violates the mutual exclusion.
	_, err = executeCommand(newRoot(), "sync", "--remote", "--endpoint", "https://example.com", "--file", "f.txt")
	if err == nil || !strings.Contains(err.Error(), `"remote-excludes-file"`) {
		t.Fatalf("expected remote-excludes-file violation, got: %v", err)
	}
}

func TestFlagConstraintCompletion(t *testing.T) {
	root := &Command{Use: "root"}
	root.AddCommand(newRemoteCmd(emptyRun))

	// With --remote already passed, --endpoint must still be suggested while
	// --file must be filtered out.
	output, err := executeCommand(root, ShellCompRequestCmd, "sync", "--remote", "--")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "--endpoint") {
		t.Errorf("completion should still suggest --endpoint, got:\n%s", output)
	}
	if strings.Contains(output, "--file") {
		t.Errorf("completion must not suggest --file after --remote, got:\n%s", output)
	}
}

func TestFlagConstraintCompletionKeepsUnknownAndPendingFlags(t *testing.T) {
	c := &Command{Use: "c", Run: emptyRun}
	c.Flags().Bool("a", false, "")
	c.Flags().Bool("b", false, "")
	c.Flags().Bool("other", false, "")
	if err := c.AddFlagConstraint("a-implies-b", Implies(Present("a"), Present("b"))); err != nil {
		t.Fatal(err)
	}
	root := &Command{Use: "root"}
	root.AddCommand(c)

	// Nothing passed yet: every flag can still lead to a valid combination.
	output, err := executeCommand(root, ShellCompRequestCmd, "c", "--")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"--a", "--b", "--other"} {
		if !strings.Contains(output, want) {
			t.Errorf("completion should suggest %s, got:\n%s", want, output)
		}
	}
}

func TestFlagConstraintExplicitFalseCountsAsPresent(t *testing.T) {
	newCmd := func(runFunc func(*Command, []string)) *Command {
		c := &Command{
			Use: "c",
			Run: runFunc,
		}
		c.Flags().Bool("enabled", true, "")
		c.Flags().String("endpoint", "", "")
		if err := c.AddFlagConstraint("enabled-requires-endpoint", Implies(Present("enabled"), Present("endpoint"))); err != nil {
			t.Fatal(err)
		}
		root := &Command{Use: "root"}
		root.AddCommand(c)
		return root
	}

	// Explicitly passing --enabled=false still counts as present.
	ran := false
	if _, err := executeCommand(newCmd(func(*Command, []string) { ran = true }), "c", "--enabled=false"); err == nil {
		t.Fatal("expected an error: --enabled=false is explicitly present")
	}
	if ran {
		t.Error("Run must not be called when a flag constraint is violated")
	}

	// Relying on the default value does not count as present.
	ran = false
	if _, err := executeCommand(newCmd(func(*Command, []string) { ran = true }), "c"); err != nil {
		t.Fatalf("default value must not count as present, got: %v", err)
	}
	if !ran {
		t.Error("Run should have been called")
	}
}

func TestFlagConstraintShadowing(t *testing.T) {
	var persistentConfig *Command
	newRoot := func() *Command {
		root := &Command{Use: "root"}
		root.PersistentFlags().String("config", "", "parent persistent config")

		child := &Command{Use: "child", Run: emptyRun}
		child.Flags().String("config", "", "local config shadows the persistent one")
		child.Flags().String("token", "", "")
		if err := child.AddFlagConstraint("config-requires-token", Implies(Present("config"), Present("token"))); err != nil {
			t.Fatal(err)
		}
		root.AddCommand(child)
		persistentConfig = root
		return root
	}

	// The local flag shadows the inherited persistent one; the constraint must
	// bind to the local flag.
	if _, err := executeCommand(newRoot(), "child", "--config", "local.toml"); err == nil {
		t.Fatal("expected an error: local --config was set without --token")
	} else if !strings.Contains(err.Error(), "config") || !strings.Contains(err.Error(), "token") {
		t.Fatalf("error should mention the bound flag names, got: %v", err)
	}
	if persistentConfig.PersistentFlags().Lookup("config").Changed {
		t.Fatal("the constraint must bind to the local flag, not the shadowed persistent one")
	}
	if _, err := executeCommand(newRoot(), "child", "--config", "local.toml", "--token", "t"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlagConstraintInheritedPersistentFlag(t *testing.T) {
	newRoot := func() *Command {
		root := &Command{Use: "root"}
		root.PersistentFlags().Bool("verbose", false, "")

		child := &Command{Use: "child", Run: emptyRun}
		child.Flags().String("output", "", "")
		if err := child.AddFlagConstraint("verbose-requires-output", Implies(Present("verbose"), Present("output"))); err != nil {
			t.Fatal(err)
		}
		root.AddCommand(child)
		return root
	}

	// The constraint binds to the inherited persistent flag.
	if _, err := executeCommand(newRoot(), "child", "--verbose"); err == nil {
		t.Fatal("expected an error: inherited --verbose was set without --output")
	}
	if _, err := executeCommand(newRoot(), "child", "--verbose", "--output", "o"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlagConstraintMutuallyImpossibleRules(t *testing.T) {
	newRoot := func() *Command {
		c := &Command{Use: "c", Run: emptyRun}
		c.Flags().Bool("a", false, "")
		c.Flags().Bool("b", false, "")
		if err := c.AddFlagConstraint("a-requires-b", Implies(Present("a"), Present("b"))); err != nil {
			t.Fatal(err)
		}
		if err := c.AddFlagConstraint("b-excludes-a", Implies(Present("b"), Not(Present("a")))); err != nil {
			t.Fatal(err)
		}
		root := &Command{Use: "root"}
		root.AddCommand(c)
		return root
	}

	// Setting only a violates the first rule.
	if _, err := executeCommand(newRoot(), "c", "--a"); err == nil || !strings.Contains(err.Error(), `"a-requires-b"`) {
		t.Fatalf("expected a-requires-b violation, got: %v", err)
	}
	// Setting both violates the second rule.
	if _, err := executeCommand(newRoot(), "c", "--a", "--b"); err == nil || !strings.Contains(err.Error(), `"b-excludes-a"`) {
		t.Fatalf("expected b-excludes-a violation, got: %v", err)
	}
	// Once --a is passed, completion must not suggest --b anymore.
	output, err := executeCommand(newRoot(), ShellCompRequestCmd, "c", "--a", "--")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(output, "--b") {
		t.Errorf("completion must not suggest --b after --a, got:\n%s", output)
	}
}

func TestFlagConstraintSharedFlagAcrossRules(t *testing.T) {
	c := &Command{Use: "c", Run: emptyRun}
	c.Flags().Bool("shared", false, "")
	c.Flags().String("x", "", "")
	c.Flags().String("y", "", "")
	if err := c.AddFlagConstraint("shared-requires-x", Implies(Present("shared"), Present("x"))); err != nil {
		t.Fatal(err)
	}
	if err := c.AddFlagConstraint("shared-requires-y", Implies(Present("shared"), Present("y"))); err != nil {
		t.Fatal(err)
	}
	root := &Command{Use: "root"}
	root.AddCommand(c)

	_, err := executeCommand(root, "c", "--shared")
	if err == nil {
		t.Fatal("expected violations")
	}
	first := strings.Index(err.Error(), `"shared-requires-x"`)
	second := strings.Index(err.Error(), `"shared-requires-y"`)
	if first < 0 || second < 0 {
		t.Fatalf("error should mention both rules, got: %v", err)
	}
	if first > second {
		t.Fatalf("violations must be reported in rule declaration order, got: %v", err)
	}
}

func TestFlagConstraintRegistrationErrors(t *testing.T) {
	c := &Command{Use: "c", Run: emptyRun}
	c.Flags().Bool("a", false, "")

	if err := c.AddFlagConstraint("empty-all", All()); err == nil {
		t.Error("empty All must be rejected at registration")
	}
	if err := c.AddFlagConstraint("empty-any", Any()); err == nil {
		t.Error("empty Any must be rejected at registration")
	}
	if err := c.AddFlagConstraint("ok", Present("a")); err != nil {
		t.Fatal(err)
	}
	if err := c.AddFlagConstraint("ok", Present("a")); err == nil {
		t.Error("duplicate rule ID must be rejected")
	}
	if err := c.AddFlagConstraint("", Present("a")); err == nil {
		t.Error("empty rule ID must be rejected")
	}
	if err := c.AddFlagConstraint("nil-expr", nil); err == nil {
		t.Error("nil expression must be rejected")
	}

	// Oversized expressions are rejected at registration.
	big := make([]FlagExpr, 0, MaxFlagConstraintExprSize+1)
	for i := 0; i < MaxFlagConstraintExprSize+1; i++ {
		big = append(big, Present("a"))
	}
	if err := c.AddFlagConstraint("too-big", All(big...)); err == nil {
		t.Error("oversized expression must be rejected at registration")
	}
}

func TestFlagConstraintResolutionErrors(t *testing.T) {
	// Unknown flag.
	c1 := &Command{Use: "c1", Run: emptyRun}
	if err := c1.AddFlagConstraint("bad-flag", Present("nope")); err != nil {
		t.Fatal(err)
	}
	root1 := &Command{Use: "root"}
	root1.AddCommand(c1)
	if _, err := executeCommand(root1, "c1"); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("expected unknown flag error, got: %v", err)
	}

	// Unknown rule reference.
	c2 := &Command{Use: "c2", Run: emptyRun}
	c2.Flags().Bool("a", false, "")
	if err := c2.AddFlagConstraint("bad-ref", Ref("missing")); err != nil {
		t.Fatal(err)
	}
	root2 := &Command{Use: "root"}
	root2.AddCommand(c2)
	if _, err := executeCommand(root2, "c2"); err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("expected unknown rule reference error, got: %v", err)
	}

	// Circular references.
	c3 := &Command{Use: "c3", Run: emptyRun}
	c3.Flags().Bool("a", false, "")
	if err := c3.AddFlagConstraint("rule-a", Ref("rule-b")); err != nil {
		t.Fatal(err)
	}
	if err := c3.AddFlagConstraint("rule-b", Ref("rule-a")); err != nil {
		t.Fatal(err)
	}
	root3 := &Command{Use: "root"}
	root3.AddCommand(c3)
	if _, err := executeCommand(root3, "c3"); err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected circular reference error, got: %v", err)
	}
}

func TestFlagConstraintRefRule(t *testing.T) {
	newRoot := func() *Command {
		c := &Command{Use: "c", Run: emptyRun}
		c.Flags().Bool("remote", false, "")
		c.Flags().String("endpoint", "", "")
		if err := c.AddFlagConstraint("endpoint-set", Present("endpoint")); err != nil {
			t.Fatal(err)
		}
		if err := c.AddFlagConstraint("remote-requires-endpoint", Implies(Present("remote"), Ref("endpoint-set"))); err != nil {
			t.Fatal(err)
		}
		root := &Command{Use: "root"}
		root.AddCommand(c)
		return root
	}

	if _, err := executeCommand(newRoot(), "c", "--remote"); err == nil {
		t.Fatal("expected an error through the referenced rule")
	}
	if _, err := executeCommand(newRoot(), "c", "--remote", "--endpoint", "e"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlagConstraintCompletionDoesNotRunCallbacks(t *testing.T) {
	ran := false
	preRan := false
	c := &Command{
		Use: "c",
		PreRun: func(*Command, []string) {
			preRan = true
		},
		Run: func(*Command, []string) { ran = true },
	}
	c.Flags().Bool("remote", false, "")
	c.Flags().String("endpoint", "", "")
	c.Flags().String("file", "", "")
	if err := c.AddFlagConstraint("remote-requires-endpoint", Implies(Present("remote"), Present("endpoint"))); err != nil {
		t.Fatal(err)
	}
	if err := c.AddFlagConstraint("remote-excludes-file", Implies(Present("remote"), Not(Present("file")))); err != nil {
		t.Fatal(err)
	}
	root := &Command{Use: "root"}
	root.AddCommand(c)

	if _, err := executeCommand(root, ShellCompRequestCmd, "c", "--remote", "--"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran || preRan {
		t.Error("completion must not execute Run, PreRun or other business callbacks")
	}
}
