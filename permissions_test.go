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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// recordingChecker keeps track of the plans it is asked to authorize. It only
// ever returns (a subset of) the required permissions and never records the
// caller's granted set anywhere visible.
type recordingChecker struct {
	granted   map[string]bool
	invoked   int
	lastPlan  PermissionPlan
	plansSeen []PermissionPlan
	err       error
}

func newRecordingChecker(granted ...string) *recordingChecker {
	set := make(map[string]bool, len(granted))
	for _, p := range granted {
		set[p] = true
	}
	return &recordingChecker{granted: set}
}

func (r *recordingChecker) CheckPermissions(plan PermissionPlan) ([]string, error) {
	r.invoked++
	r.lastPlan = plan
	r.plansSeen = append(r.plansSeen, plan)
	if r.err != nil {
		return nil, r.err
	}
	var missing []string
	for _, p := range plan.Required {
		if !r.granted[p] {
			missing = append(missing, p)
		}
	}
	return missing, nil
}

// permissionCounters tracks every business hook invoked while building the
// command tree used by the tests.
type permissionCounters struct {
	rootPersistentPreRun int
	childPreRun          int
	childRun             int
	leafPreRun           int
	leafRun              int
}

func newPermissionTree(t *testing.T) (*Command, *permissionCounters) {
	t.Helper()
	counters := &permissionCounters{}

	root := &Command{
		Use: "app",
		Annotations: map[string]string{
			PermissionRequiredAnnotation: "read",
		},
		PersistentPreRun: func(cmd *Command, args []string) {
			counters.rootPersistentPreRun++
		},
		Run: func(cmd *Command, args []string) {},
	}

	child := &Command{
		Use:     "push",
		Aliases: []string{"publish"},
		Annotations: map[string]string{
			PermissionRequiredAnnotation: "write",
		},
		PreRun: func(cmd *Command, args []string) {
			counters.childPreRun++
		},
		Run: func(cmd *Command, args []string) {
			counters.childRun++
		},
	}

	// A nested command demonstrates that parent requirements are inherited
	// through more than one level and that children can only add to them.
	grand := &Command{
		Use: "deep",
		Annotations: map[string]string{
			PermissionRequiredAnnotation: "admin",
		},
		PreRun: func(cmd *Command, args []string) {
			counters.leafPreRun++
		},
		Run: func(cmd *Command, args []string) {
			counters.leafRun++
		},
	}

	root.AddCommand(child)
	child.AddCommand(grand)

	// Silence noise on the assertion path; errors are still returned.
	for _, c := range []*Command{root, child, grand} {
		c.SilenceErrors = true
		c.SilenceUsage = true
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
	}

	return root, counters
}

func executeWith(root *Command, args ...string) error {
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	return root.Execute()
}

// TestPermissionDeniedBlocksHooks is the headline acceptance case: the root
// requires "read", the child adds "write". Holding only "read" must reject the
// child without invoking any PreRun/Run hook.
func TestPermissionDeniedBlocksHooks(t *testing.T) {
	root, counters := newPermissionTree(t)
	checker := newRecordingChecker("read")
	root.SetPermissionChecker(checker)

	err := executeWith(root, "push")
	if err == nil {
		t.Fatal("expected permission denial, got nil")
	}

	denied, ok := AsPermissionDenied(err)
	if !ok {
		t.Fatalf("expected *PermissionDeniedError, got %T: %v", err, err)
	}
	if got, want := denied.Missing, []string{"write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing permissions = %v, want %v", got, want)
	}
	if !strings.Contains(err.Error(), "write") {
		t.Fatalf("error must name the missing permission: %v", err)
	}
	// The granted set ("read") must not be leaked by the denial message.
	if strings.Contains(err.Error(), "granted") || strings.Contains(err.Error(), "held") {
		t.Fatalf("error leaks caller permission details: %v", err)
	}

	if counters.rootPersistentPreRun != 0 || counters.childPreRun != 0 || counters.childRun != 0 {
		t.Fatalf("no business hook may run on denial, got %+v", counters)
	}
	if checker.invoked != 1 {
		t.Fatalf("checker invoked %d times, want 1", checker.invoked)
	}
}

// TestPermissionApprovedRunsHooks verifies that holding both inherited and
// own requirements allows the full lifecycle to execute.
func TestPermissionApprovedRunsHooks(t *testing.T) {
	root, counters := newPermissionTree(t)
	root.SetPermissionChecker(newRecordingChecker("read", "write"))

	if err := executeWith(root, "push"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if counters.rootPersistentPreRun != 1 || counters.childPreRun != 1 || counters.childRun != 1 {
		t.Fatalf("expected all hooks to run once, got %+v", counters)
	}
}

// TestPermissionRootOnlyRequirement covers the root command itself: "read"
// alone is enough at the root.
func TestPermissionRootOnlyRequirement(t *testing.T) {
	root, counters := newPermissionTree(t)
	root.SetPermissionChecker(newRecordingChecker("read"))

	if err := executeWith(root); err != nil {
		t.Fatalf("root with read should pass, got %v", err)
	}
	if counters.rootPersistentPreRun != 1 {
		t.Fatalf("root persistent pre-run expected once, got %d", counters.rootPersistentPreRun)
	}
}

// TestPermissionAliasSameIdentity ensures an alias resolves to the very same
// identity and therefore produces the same plan and the same decision.
func TestPermissionAliasSameIdentity(t *testing.T) {
	root, _ := newPermissionTree(t)
	child, _, err := root.Find([]string{"publish"})
	if err != nil {
		t.Fatal(err)
	}
	if child.Name() != "push" {
		t.Fatalf("alias must resolve to canonical command, got %q", child.Name())
	}

	byAlias := child.EffectivePermissions()
	canonical, _, err := root.Find([]string{"push"})
	if err != nil {
		t.Fatal(err)
	}
	byName := canonical.EffectivePermissions()
	if !reflect.DeepEqual(byAlias, byName) {
		t.Fatalf("alias plan %+v differs from canonical %+v", byAlias, byName)
	}
	if byAlias.Identity != "app push" {
		t.Fatalf("identity must be canonical, got %q", byAlias.Identity)
	}
	if got, want := byAlias.Required, []string{"read", "write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective required = %v, want %v", got, want)
	}

	// Execution through the alias must be denied under the same granted set.
	root.SetPermissionChecker(newRecordingChecker("read"))
	if err := executeWith(root, "publish"); err == nil {
		t.Fatal("alias execution should be denied when write is missing")
	}
}

// TestPermissionNestedInheritance verifies nested annotations accumulate from
// the root (read) through the child (write) to the leaf (admin), and that a
// child cannot drop a parent requirement.
func TestPermissionNestedInheritance(t *testing.T) {
	root, counters := newPermissionTree(t)
	leaf, _, err := root.Find([]string{"push", "deep"})
	if err != nil {
		t.Fatal(err)
	}
	plan := leaf.EffectivePermissions()
	if got, want := plan.Required, []string{"read", "write", "admin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nested required = %v, want %v", got, want)
	}
	if plan.Identity != "app push deep" {
		t.Fatalf("unexpected identity %q", plan.Identity)
	}

	// write+read without admin -> denied.
	root.SetPermissionChecker(newRecordingChecker("read", "write"))
	if err := executeWith(root, "push", "deep"); err == nil {
		t.Fatal("expected denial for missing admin")
	}
	if counters.leafRun != 0 {
		t.Fatalf("leaf run must not execute, got %d", counters.leafRun)
	}

	// Granting all three executes.
	root.SetPermissionChecker(newRecordingChecker("read", "write", "admin"))
	if err := executeWith(root, "push", "deep"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if counters.leafPreRun != 1 || counters.leafRun != 1 {
		t.Fatalf("leaf hooks expected once each, got %+v", counters)
	}
}

// TestPermissionWriteInheritanceRule documents that requirements are
// additive only: the child's own "write" does not replace the inherited
// "read", and duplicates are merged.
func TestPermissionWriteInheritanceRule(t *testing.T) {
	root := &Command{
		Use: "svc",
		Annotations: map[string]string{
			PermissionRequiredAnnotation: "read, read",
		},
	}
	child := &Command{
		Use: "write-cmd",
		Annotations: map[string]string{
			// Declaring only "write" must not erase inherited "read".
			PermissionRequiredAnnotation: "write",
		},
		Run: func(cmd *Command, args []string) {},
	}
	root.AddCommand(child)

	plan := child.EffectivePermissions()
	if got, want := plan.Required, []string{"read", "write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective = %v, want %v", got, want)
	}

	checker := newRecordingChecker("write") // intentionally missing inherited read
	root.SetPermissionChecker(checker)
	child.SilenceErrors = true
	child.SilenceUsage = true
	err := executeWith(root, "write-cmd")
	denied, ok := AsPermissionDenied(err)
	if !ok {
		t.Fatalf("expected PermissionDeniedError, got %v", err)
	}
	if got, want := denied.Missing, []string{"read"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
}

// TestPermissionHelpPathExempt ensures the built-in help command and the
// --help flag never invoke the business checker nor business hooks.
func TestPermissionHelpPathExempt(t *testing.T) {
	root, counters := newPermissionTree(t)
	checker := newRecordingChecker() // grants nothing
	root.SetPermissionChecker(checker)

	// "app help" -> read-only display path.
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help should never be denied: %v", err)
	}

	// "app push --help" must short-circuit before the permission layer.
	root.SetArgs([]string{"push", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help should never be denied: %v", err)
	}

	if checker.invoked != 0 {
		t.Fatalf("checker must not run on help paths, invoked %d times", checker.invoked)
	}
	if counters.childRun != 0 || counters.childPreRun != 0 || counters.rootPersistentPreRun != 0 {
		t.Fatalf("business hooks must not run for help, got %+v", counters)
	}

	helpCmd, _, err := root.Find([]string{"help"})
	if err != nil {
		t.Fatal(err)
	}
	if !helpCmd.PermissionExempt() {
		t.Fatal("built-in help command must be permission exempt")
	}
}

// TestPermissionCompletionExempt ensures the hidden __complete command, its
// __completeNoDesc alias and the default "completion" command tree are all
// read-only: the checker is never invoked while completion still works with
// the inherited PersistentPreRun semantics.
func TestPermissionCompletionExempt(t *testing.T) {
	root, counters := newPermissionTree(t)
	checker := newRecordingChecker() // grants nothing
	root.SetPermissionChecker(checker)

	// Simulate shell completion requests, exactly like the shell would.
	for _, args := range [][]string{
		{"__complete", "pus"},
		{"__completeNoDesc", "pus"},
		{"__complete", "push", ""},
	} {
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("completion request %v should not be denied: %v", args, err)
		}
	}

	if checker.invoked != 0 {
		t.Fatalf("checker must not run during completion, invoked %d times", checker.invoked)
	}
	if counters.childRun != 0 {
		t.Fatalf("business run must not execute during completion, got %d", counters.childRun)
	}
	// Compatibility: unlike the help path, shell completion keeps invoking the
	// inherited PersistentPreRun (once per request, standard cobra semantics).
	if counters.rootPersistentPreRun != 3 {
		t.Fatalf("completion must preserve inherited PersistentPreRun, got %d",
			counters.rootPersistentPreRun)
	}

	// The default "completion" command and shell sub-commands are exempt too.
	root.SetArgs([]string{"completion", "bash"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("completion script generation should not be denied: %v", err)
	}
	if checker.invoked != 0 {
		t.Fatalf("checker must not run for completion script generation, invoked %d", checker.invoked)
	}
	for _, path := range [][]string{
		{"completion"},
		{"completion", "bash"},
		{"completion", "zsh"},
		{"completion", "fish"},
		{"completion", "powershell"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if !cmd.PermissionExempt() {
			t.Fatalf("completion command %v must be permission exempt", path)
		}
	}
}

// TestPermissionPersistentPreRunCompatibility verifies that an approved plan
// keeps the traditional persistent pre-run semantics: the root persistent
// hook runs for the child, and with EnableTraverseRunHooks every ancestor's
// persistent hook runs.
func TestPermissionPersistentPreRunCompatibility(t *testing.T) {
	build := func() (*Command, *permissionCounters) {
		return newPermissionTree(t)
	}

	// Default semantics: nearest PersistentPreRun only.
	root, counters := build()
	root.SetPermissionChecker(newRecordingChecker("read", "write", "admin"))
	EnableTraverseRunHooks = false
	if err := executeWith(root, "push", "deep"); err != nil {
		t.Fatal(err)
	}
	if counters.rootPersistentPreRun != 1 {
		t.Fatalf("root persistent pre-run should run once, got %d", counters.rootPersistentPreRun)
	}

	// Traverse semantics: all ancestor persistent hooks still run after
	// authorization.
	root2, counters2 := build()
	root2.SetPermissionChecker(newRecordingChecker("read", "write", "admin"))
	EnableTraverseRunHooks = true
	defer func() { EnableTraverseRunHooks = false }()
	if err := executeWith(root2, "push", "deep"); err != nil {
		t.Fatal(err)
	}
	if counters2.rootPersistentPreRun != 1 {
		t.Fatalf("traverse mode should keep persistent pre-run working, got %d", counters2.rootPersistentPreRun)
	}
	if counters2.leafPreRun != 1 || counters2.leafRun != 1 {
		t.Fatalf("leaf hooks should run, got %+v", counters2)
	}
}

// TestPermissionNoCheckerBackwardCompatible verifies that without a checker
// Cobra preserves its historical behavior and does not gate execution.
func TestPermissionNoCheckerBackwardCompatible(t *testing.T) {
	root, counters := newPermissionTree(t)
	// No SetPermissionChecker call.
	if err := executeWith(root, "push"); err != nil {
		t.Fatalf("without a checker execution must proceed, got %v", err)
	}
	if counters.childRun != 1 {
		t.Fatalf("child run expected once, got %d", counters.childRun)
	}
}

// TestPermissionCheckerInheritance verifies the checker is resolved from the
// closest ancestor and can be overridden per subtree.
func TestPermissionCheckerInheritance(t *testing.T) {
	root, _ := newPermissionTree(t)
	rootChecker := newRecordingChecker("read")
	root.SetPermissionChecker(rootChecker)

	child, _, err := root.Find([]string{"push"})
	if err != nil {
		t.Fatal(err)
	}
	childChecker := newRecordingChecker("read", "write")
	child.SetPermissionChecker(childChecker)

	if err := executeWith(root, "push"); err != nil {
		t.Fatalf("child override should approve, got %v", err)
	}
	if rootChecker.invoked != 0 {
		t.Fatal("root checker must be shadowed by child checker")
	}
	if childChecker.invoked != 1 {
		t.Fatalf("child checker should be used once, got %d", childChecker.invoked)
	}
	// The child sees the full effective plan, not just its own annotation.
	if got, want := childChecker.lastPlan.Required, []string{"read", "write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inherited plan = %v, want %v", got, want)
	}
}

// TestPermissionExemptNotInherited ensures a command marked exempt does not
// exempt its children.
func TestPermissionExemptNotInherited(t *testing.T) {
	root := &Command{Use: "ro"}
	display := &Command{
		Use: "show",
		Annotations: map[string]string{
			PermissionExemptAnnotation: "true",
		},
	}
	hidden := &Command{
		Use: "mutate",
		Annotations: map[string]string{
			PermissionRequiredAnnotation: "write",
		},
	}
	display.AddCommand(hidden)
	root.AddCommand(display)
	for _, c := range []*Command{root, display, hidden} {
		c.SilenceErrors = true
		c.SilenceUsage = true
	}
	root.SetPermissionChecker(newRecordingChecker())

	if !display.PermissionExempt() {
		t.Fatal("annotated command must be exempt")
	}
	if hidden.EffectivePermissions().Exempt {
		t.Fatal("exemption must not be inherited by child commands")
	}
}

// TestStaticPermissionChecker covers the built-in checker helper.
func TestStaticPermissionChecker(t *testing.T) {
	plan := PermissionPlan{Identity: "app push", Required: []string{"read", "write"}}

	if missing, _ := NewStaticPermissionChecker("read", "write").CheckPermissions(plan); len(missing) != 0 {
		t.Fatalf("fully granted plan must have no missing perms, got %v", missing)
	}
	missing, err := NewStaticPermissionChecker("read").CheckPermissions(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := missing, []string{"write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
}

// TestPermissionTraverseChildren ensures the plan is enforced on the resolved
// target command even when TraverseChildren flag parsing is in use, including
// invocation through an alias.
func TestPermissionTraverseChildren(t *testing.T) {
	root, counters := newPermissionTree(t)
	root.TraverseChildren = true
	root.SetPermissionChecker(newRecordingChecker("read"))

	err := executeWith(root, "publish")
	if err == nil {
		t.Fatal("alias traversal should be denied without write")
	}
	if _, ok := AsPermissionDenied(err); !ok {
		t.Fatalf("expected PermissionDeniedError, got %T %v", err, err)
	}
	if counters.rootPersistentPreRun != 0 || counters.childRun != 0 {
		t.Fatalf("no hook may run on denial under traversal, got %+v", counters)
	}

	root.SetPermissionChecker(newRecordingChecker("read", "write"))
	if err := executeWith(root, "publish"); err != nil {
		t.Fatalf("authorized alias traversal should succeed, got %v", err)
	}
	if counters.childRun != 1 {
		t.Fatalf("child run expected once, got %d", counters.childRun)
	}
}

// TestPermissionCheckerErrorPassthrough verifies that infrastructure errors
// returned by the checker stop execution before any business hook, and are
// surfaced unchanged.
func TestPermissionCheckerErrorPassthrough(t *testing.T) {
	root, counters := newPermissionTree(t)
	checkerErr := fmt.Errorf("policy service unavailable")
	root.SetPermissionChecker(PermissionCheckerFunc(func(PermissionPlan) ([]string, error) {
		return nil, checkerErr
	}))

	err := executeWith(root, "push")
	if !errors.Is(err, checkerErr) {
		t.Fatalf("expected checker error to surface, got %v", err)
	}
	if counters.childRun != 0 || counters.childPreRun != 0 || counters.rootPersistentPreRun != 0 {
		t.Fatalf("no hook may run when the checker errors, got %+v", counters)
	}
}

// TestPermissionDeniedMessageLeakSafety ensures the denial message reports the
// missing required permission names but never prints the caller's full
// permission set, even when the checker tries to smuggle extra names.
func TestPermissionDeniedMessageLeakSafety(t *testing.T) {
	root, _ := newPermissionTree(t)
	// A buggy/hostile checker returning names that are not part of the plan.
	root.SetPermissionChecker(PermissionCheckerFunc(func(PermissionPlan) ([]string, error) {
		return []string{"write", "secret-granted-perm"}, nil
	}))

	err := executeWith(root, "push")
	denied, ok := AsPermissionDenied(err)
	if !ok {
		t.Fatalf("expected PermissionDeniedError, got %v", err)
	}
	if !reflect.DeepEqual(denied.Missing, []string{"write"}) {
		t.Fatalf("only required perms may be reported, got %v", denied.Missing)
	}
	if strings.Contains(err.Error(), "secret-granted-perm") {
		t.Fatalf("denial leaked a permission outside the plan: %v", err)
	}
}

// ExampleCommand_effectivePermissions is the small, self-contained example
// documenting how to declare inherited requirements and authorize a plan.
func ExampleCommand_effectivePermissions() {
	root := &Command{
		Use:         "app",
		Annotations: map[string]string{PermissionRequiredAnnotation: "read"},
	}
	push := &Command{
		Use:         "push",
		Annotations: map[string]string{PermissionRequiredAnnotation: "write"},
	}
	root.AddCommand(push)

	// The child's plan inherits the parent's "read" and adds "write".
	plan := push.EffectivePermissions()
	fmt.Println(plan.Identity, strings.Join(plan.Required, ","))

	// A caller holding only "read" is rejected; holding both is approved.
	root.SetPermissionChecker(NewStaticPermissionChecker("read"))
	push.RunE = func(*Command, []string) error { return nil }
	push.PreRunE = func(*Command, []string) error { return nil }
	push.SilenceErrors = true
	push.SilenceUsage = true
	root.SetArgs([]string{"push"})
	err := root.Execute()
	denied, _ := AsPermissionDenied(err)
	fmt.Println("denied missing:", strings.Join(denied.Missing, ","))

	root.SetPermissionChecker(NewStaticPermissionChecker("read", "write"))
	root.SetArgs([]string{"push"})
	fmt.Println("approved:", root.Execute() == nil)
	// Output:
	// app push read,write
	// denied missing: write
	// approved: true
}
