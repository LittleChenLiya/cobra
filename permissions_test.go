// Copyright 2013-2024 The Cobra Authors
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
	"strings"
	"testing"
)

// permCounters records how often the business callbacks of a command tree
// were invoked, so tests can prove that rejected commands run nothing.
type permCounters struct {
	persistentPreRun int
	preRun           int
	run              int
	postRun          int
}

// buildPermTree builds the acceptance tree: root requires "read", its child
// (also reachable through the "kid" alias) requires "write", and a nested
// grandchild requires "execute". Every business callback increments a counter.
func buildPermTree(checker PermissionChecker) (*Command, *permCounters) {
	counters := &permCounters{}

	root := &Command{
		Use:   "root",
		Short: "root command",
		Annotations: map[string]string{
			PermissionsAnnotation: "read",
		},
		PersistentPreRun: func(cmd *Command, args []string) {
			counters.persistentPreRun++
		},
		Run: func(cmd *Command, args []string) {
			counters.run++
		},
	}
	root.PermissionChecker = checker

	child := &Command{
		Use:     "child",
		Aliases: []string{"kid"},
		Short:   "child command",
		Annotations: map[string]string{
			PermissionsAnnotation: "write",
		},
		PreRun: func(cmd *Command, args []string) {
			counters.preRun++
		},
		Run: func(cmd *Command, args []string) {
			counters.run++
		},
		PostRun: func(cmd *Command, args []string) {
			counters.postRun++
		},
	}
	root.AddCommand(child)

	grandchild := &Command{
		Use:   "grandchild",
		Short: "grandchild command",
		Annotations: map[string]string{
			PermissionsAnnotation: "execute",
		},
		Run: func(cmd *Command, args []string) {
			counters.run++
		},
	}
	child.AddCommand(grandchild)

	return root, counters
}

// grant returns a checker that grants exactly the given permissions.
func grant(perms ...string) PermissionChecker {
	return PermissionCheckerFunc(func(required []string) []string {
		return perms
	})
}

func executePerm(t *testing.T, root *Command, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	return root.Execute()
}

func TestPermissionPlanInheritance(t *testing.T) {
	root, _ := buildPermTree(nil)
	child, _, err := root.Find([]string{"child"})
	if err != nil {
		t.Fatalf("Find child: %v", err)
	}
	grandchild, _, err := root.Find([]string{"child", "grandchild"})
	if err != nil {
		t.Fatalf("Find grandchild: %v", err)
	}

	// Each command only reports the permissions declared directly on it.
	if got, want := strings.Join(root.RequiredPermissions(), ","), "read"; got != want {
		t.Errorf("root RequiredPermissions = %q, want %q", got, want)
	}
	if got, want := strings.Join(child.RequiredPermissions(), ","), "write"; got != want {
		t.Errorf("child RequiredPermissions = %q, want %q", got, want)
	}

	// The effective plan inherits ancestor requirements, root first.
	assertPlan := func(cmd *Command, want []string) {
		t.Helper()
		got := cmd.PermissionPlan()
		if len(got) != len(want) {
			t.Fatalf("PermissionPlan(%q) = %v, want %v", cmd.CommandPath(), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("PermissionPlan(%q) = %v, want %v", cmd.CommandPath(), got, want)
			}
		}
	}
	assertPlan(root, []string{"read"})
	assertPlan(child, []string{"read", "write"})
	// Nested annotations: the grandchild adds "execute" on top of both.
	assertPlan(grandchild, []string{"read", "write", "execute"})

	// A child can only add, never remove: "read" stays in the plan even
	// though the child only declares "write".
	if !child.HasPermission("read") {
		t.Error("child plan lost inherited 'read' permission")
	}
	if child.HasPermission("execute") {
		t.Error("child plan unexpectedly contains 'execute' (only the grandchild declares it)")
	}
	if !grandchild.HasPermission("read") || !grandchild.HasPermission("write") {
		t.Error("grandchild plan lost inherited permissions")
	}
}

func TestPermissionAnnotationParsing(t *testing.T) {
	c := &Command{Annotations: map[string]string{
		PermissionsAnnotation: " read, write ,read,,execute , ",
	}}
	got := c.RequiredPermissions()
	want := []string{"read", "write", "execute"}
	if len(got) != len(want) {
		t.Fatalf("RequiredPermissions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RequiredPermissions = %v, want %v", got, want)
		}
	}
}

func TestPermissionRejectedMissingOnly(t *testing.T) {
	// The caller holds "read" and an unrelated "admin" permission, but is
	// missing "write".
	root, counters := buildPermTree(grant("read", "admin"))

	var required []string
	root.PermissionChecker = PermissionCheckerFunc(func(got []string) []string {
		required = append([]string(nil), got...)
		return []string{"read", "admin"}
	})

	err := executePerm(t, root, "child")
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}

	var missing *MissingPermissionsError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %T (%v), want *MissingPermissionsError", err, err)
	}
	if got, want := strings.Join(missing.Missing, ","), "write"; got != want {
		t.Errorf("missing = %q, want %q", got, want)
	}
	if got, want := strings.Join(required, ","), "read,write"; got != want {
		t.Errorf("checker required = %q, want %q", got, want)
	}

	// The error must only name the missing permission; it must not leak the
	// fact that the caller also holds "admin".
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("error %q does not mention missing permission 'write'", err.Error())
	}
	if strings.Contains(err.Error(), "admin") {
		t.Errorf("error %q leaks unrelated held permission 'admin'", err.Error())
	}

	// No business callback may run when the plan is rejected.
	if counters.persistentPreRun != 0 || counters.preRun != 0 || counters.run != 0 || counters.postRun != 0 {
		t.Errorf("counters = %+v, want all zero after rejection", *counters)
	}
}

func TestPermissionGrantedExecutes(t *testing.T) {
	root, counters := buildPermTree(grant("read", "write"))

	if err := executePerm(t, root, "child"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PersistentPreRun (inherited from root), the child's PreRun, Run and
	// PostRun all execute exactly once.
	if counters.persistentPreRun != 1 {
		t.Errorf("persistentPreRun count = %d, want 1", counters.persistentPreRun)
	}
	if counters.preRun != 1 {
		t.Errorf("preRun count = %d, want 1", counters.preRun)
	}
	if counters.run != 1 {
		t.Errorf("run count = %d, want 1", counters.run)
	}
	if counters.postRun != 1 {
		t.Errorf("postRun count = %d, want 1", counters.postRun)
	}
}

func TestPermissionAliasSamePlan(t *testing.T) {
	root, _ := buildPermTree(grant("read"))

	canonical, _, err := root.Find([]string{"child"})
	if err != nil {
		t.Fatalf("Find child: %v", err)
	}
	aliased, _, err := root.Find([]string{"kid"})
	if err != nil {
		t.Fatalf("Find kid: %v", err)
	}
	if canonical != aliased {
		t.Fatal("alias resolved to a different Command than the canonical name")
	}
	want := canonical.PermissionPlan()
	got := aliased.PermissionPlan()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("alias plan = %v, want %v", got, want)
	}

	// Invoking through the alias with insufficient permissions is rejected
	// exactly like the canonical name, with no business callbacks.
	root2, counters := buildPermTree(grant("read"))
	if err := executePerm(t, root2, "kid"); err == nil {
		t.Fatal("expected permission error for alias, got nil")
	} else if !strings.Contains(err.Error(), "write") {
		t.Errorf("alias error %q does not mention missing 'write'", err.Error())
	}
	if counters.persistentPreRun != 0 || counters.preRun != 0 || counters.run != 0 || counters.postRun != 0 {
		t.Errorf("counters = %+v, want all zero after alias rejection", *counters)
	}

	// With both permissions, the alias executes just like the canonical name.
	root3, counters3 := buildPermTree(grant("read", "write"))
	if err := executePerm(t, root3, "kid"); err != nil {
		t.Fatalf("alias execution: %v", err)
	}
	if counters3.run != 1 || counters3.preRun != 1 {
		t.Errorf("alias counters = %+v, want preRun=1 run=1", *counters3)
	}
}

func TestPermissionHelpSkips(t *testing.T) {
	// A checker that rejects everything must never be consulted for the
	// read-only help paths.
	root, counters := buildPermTree(grant())

	cases := [][]string{
		{"child", "--help"}, // help flag on the target command
		{"help", "child"},   // help command path
		{"help"},            // root help
		{"--help"},          // root help flag
	}
	for _, args := range cases {
		if err := executePerm(t, root, args...); err != nil {
			t.Errorf("help path %v: unexpected error: %v", args, err)
		}
	}
	if counters.persistentPreRun != 0 || counters.preRun != 0 || counters.run != 0 || counters.postRun != 0 {
		t.Errorf("counters = %+v, want all zero after help paths", *counters)
	}
}

func TestPermissionCompletionSkips(t *testing.T) {
	root, counters := buildPermTree(grant())

	cases := [][]string{
		{"__complete", "child", ""}, // runtime completion request
		{"completion", "bash"},      // read-only script generation
	}
	for _, args := range cases {
		if err := executePerm(t, root, args...); err != nil {
			t.Errorf("completion path %v: unexpected error: %v", args, err)
		}
	}
	if counters.persistentPreRun != 0 || counters.preRun != 0 || counters.run != 0 || counters.postRun != 0 {
		t.Errorf("counters = %+v, want all zero after completion paths", *counters)
	}
}

func TestPermissionSkipAnnotation(t *testing.T) {
	root, counters := buildPermTree(grant())
	readOnly := &Command{
		Use:     "status",
		Short:   "read-only status command",
		Aliases: []string{"st"},
		Annotations: map[string]string{
			PermissionsAnnotation:     "admin",
			PermissionsSkipAnnotation: "true",
		},
		Run: func(cmd *Command, args []string) {
			counters.run++
		},
	}
	root.AddCommand(readOnly)

	// Even though the caller holds no permissions, the skip annotation lets
	// this read-only command run.
	if err := executePerm(t, root, "status"); err != nil {
		t.Fatalf("skip-annotated command: %v", err)
	}
	if counters.run != 1 {
		t.Errorf("run count = %d, want 1", counters.run)
	}

	// The alias shares the same identity and therefore the same skip marker.
	if err := executePerm(t, root, "st"); err != nil {
		t.Fatalf("skip-annotated alias: %v", err)
	}
	if counters.run != 2 {
		t.Errorf("run count = %d, want 2", counters.run)
	}
}

func TestPermissionNoCheckerDisablesLayer(t *testing.T) {
	root, counters := buildPermTree(nil)
	if err := executePerm(t, root, "child"); err != nil {
		t.Fatalf("execution without a checker should not be gated: %v", err)
	}
	if counters.run != 1 || counters.preRun != 1 {
		t.Errorf("counters = %+v, want preRun=1 run=1", *counters)
	}
}

func TestPermissionCheckerFuncAdapter(t *testing.T) {
	fn := PermissionCheckerFunc(func(required []string) []string {
		return required
	})
	got := fn.GrantedPermissions([]string{"read", "write"})
	if len(got) != 2 || got[0] != "read" || got[1] != "write" {
		t.Errorf("adapter returned %v, want [read write]", got)
	}
}

func TestMissingPermissionsErrorMessage(t *testing.T) {
	err := &MissingPermissionsError{Missing: []string{"write", "execute"}}
	if got, want := err.Error(), "missing required permission(s): write, execute"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// ExampleCommand_PermissionPlan shows how the effective permission plan is
// inherited from the root command down to a child command. The root requires
// "read" and the child adds "write"; the child's plan contains both.
func ExampleCommand_PermissionPlan() {
	root := &Command{
		Use:         "app",
		Annotations: map[string]string{PermissionsAnnotation: "read"},
	}
	child := &Command{
		Use:         "child",
		Annotations: map[string]string{PermissionsAnnotation: "write"},
	}
	root.AddCommand(child)

	fmt.Println(root.PermissionPlan())
	fmt.Println(child.PermissionPlan())
	// Output:
	// [read]
	// [read write]
}
