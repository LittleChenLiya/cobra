// Copyright 2024 The Cobra Authors
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
	"reflect"
	"strings"
	"testing"
)

// TestAliasCheckerNameAliasConflict covers the main acceptance scenario:
// at the same level, sync's alias "s" conflicts with another command's real
// name "s"; the "s" under a different parent (tools) does not conflict.
func TestAliasCheckerNameAliasConflict(t *testing.T) {
	root := &Command{Use: "root"}
	sync := &Command{Use: "sync", Aliases: []string{"s"}, Run: emptyRun}
	realS := &Command{Use: "s", Run: emptyRun}
	tools := &Command{Use: "tools", Run: emptyRun}
	tools.AddCommand(&Command{Use: "s", Run: emptyRun})
	root.AddCommand(sync, realS, tools)

	conflicts := root.CheckAliases()
	if len(conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict, got %d: %v", len(conflicts), conflicts)
	}

	c := conflicts[0]
	if c.Parent != root {
		t.Errorf("expected parent %q, got %q", root.CommandPath(), c.Parent.CommandPath())
	}
	if c.Token != "s" {
		t.Errorf("expected token %q, got %q", "s", c.Token)
	}
	if c.First.Command != sync || c.First.Kind != AliasKindAlias {
		t.Errorf("expected first occurrence to be alias of %q, got %s of %q",
			sync.CommandPath(), c.First.Kind, c.First.Command.CommandPath())
	}
	if c.Second.Command != realS || c.Second.Kind != AliasKindName {
		t.Errorf("expected second occurrence to be name of %q, got %s of %q",
			realS.CommandPath(), c.Second.Kind, c.Second.Command.CommandPath())
	}
	if c.Type() != "name/alias conflict" {
		t.Errorf("expected type %q, got %q", "name/alias conflict", c.Type())
	}

	report := c.String()
	if !strings.Contains(report, "root sync") {
		t.Errorf("report %q does not contain path of alias owner", report)
	}
	if !strings.Contains(report, "root s") {
		t.Errorf("report %q does not contain path of name owner", report)
	}
	if strings.Contains(report, "root tools s") {
		t.Errorf("report %q must not mention the out-of-scope tools/s command", report)
	}
}

// TestAliasCheckerDuplicateDeclaration verifies that a token declared twice
// by the same command gets its own, separate diagnosis.
func TestAliasCheckerDuplicateDeclaration(t *testing.T) {
	root := &Command{Use: "root"}
	dupAlias := &Command{Use: "dup", Aliases: []string{"s", "s"}, Run: emptyRun}
	sameName := &Command{Use: "same", Aliases: []string{"same"}, Run: emptyRun}
	root.AddCommand(dupAlias, sameName)

	conflicts := root.CheckAliases()
	if len(conflicts) != 2 {
		t.Fatalf("expected 2 duplicate-declaration diagnoses, got %d: %v", len(conflicts), conflicts)
	}

	first, second := conflicts[0], conflicts[1]
	if first.Type() != "duplicate alias declaration" {
		t.Errorf("expected first type %q, got %q", "duplicate alias declaration", first.Type())
	}
	if first.Token != "s" || first.First.Command != dupAlias || first.Second.Command != dupAlias {
		t.Errorf("unexpected first conflict: %+v", first)
	}
	if second.Type() != "duplicate name/alias declaration" {
		t.Errorf("expected second type %q, got %q", "duplicate name/alias declaration", second.Type())
	}
	if second.Token != "same" || second.First.Command != sameName || second.Second.Command != sameName {
		t.Errorf("unexpected second conflict: %+v", second)
	}
	if !strings.Contains(second.String(), "root same") {
		t.Errorf("report %q does not contain the command path", second.String())
	}
}

// TestAliasCheckerHiddenCommands verifies that hidden commands participate
// in the conflict check just like visible ones.
func TestAliasCheckerHiddenCommands(t *testing.T) {
	root := &Command{Use: "root"}
	hidden := &Command{Use: "hidden", Aliases: []string{"s"}, Hidden: true, Run: emptyRun}
	visible := &Command{Use: "s", Run: emptyRun}
	root.AddCommand(hidden, visible)

	conflicts := root.CheckAliases()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict involving the hidden command, got %d: %v", len(conflicts), conflicts)
	}
	if conflicts[0].First.Command != hidden || conflicts[0].Second.Command != visible {
		t.Errorf("unexpected conflict parties: %+v", conflicts[0])
	}
}

// TestAliasCheckerDifferentBranches verifies that identical tokens under
// different parent branches are not reported as conflicts.
func TestAliasCheckerDifferentBranches(t *testing.T) {
	root := &Command{Use: "root"}
	branchA := &Command{Use: "a", Run: emptyRun}
	branchB := &Command{Use: "b", Run: emptyRun}
	branchA.AddCommand(&Command{Use: "s", Aliases: []string{"x"}, Run: emptyRun})
	branchB.AddCommand(&Command{Use: "s", Aliases: []string{"x"}, Run: emptyRun})
	root.AddCommand(branchA, branchB)

	if conflicts := root.CheckAliases(); len(conflicts) != 0 {
		t.Fatalf("expected no conflicts across branches, got %d: %v", len(conflicts), conflicts)
	}
}

// TestAliasCheckerSuggestions verifies that configured suggested aliases
// (SuggestFor) participate in the check.
func TestAliasCheckerSuggestions(t *testing.T) {
	root := &Command{Use: "root"}
	suggester := &Command{Use: "suggester", SuggestFor: []string{"s"}, Run: emptyRun}
	realS := &Command{Use: "s", Run: emptyRun}
	root.AddCommand(suggester, realS)

	conflicts := root.CheckAliases()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 name/suggestion conflict, got %d: %v", len(conflicts), conflicts)
	}
	c := conflicts[0]
	if c.Type() != "name/suggestion conflict" {
		t.Errorf("expected type %q, got %q", "name/suggestion conflict", c.Type())
	}
	if c.First.Kind != AliasKindSuggestion || c.First.Command != suggester {
		t.Errorf("expected first occurrence to be suggestion of %q, got %s of %q",
			suggester.CommandPath(), c.First.Kind, c.First.Command.CommandPath())
	}
	if c.Second.Kind != AliasKindName || c.Second.Command != realS {
		t.Errorf("expected second occurrence to be name of %q, got %s of %q",
			realS.CommandPath(), c.Second.Kind, c.Second.Command.CommandPath())
	}
}

// TestAliasCheckerRegistrationOrder verifies that conflicts are reported in
// registration order regardless of the order in which children are visited.
func TestAliasCheckerRegistrationOrder(t *testing.T) {
	root := &Command{Use: "root"}
	c1 := &Command{Use: "c1", Aliases: []string{"x"}, Run: emptyRun}
	c2 := &Command{Use: "c2", Aliases: []string{"x"}, Run: emptyRun}
	c3 := &Command{Use: "c3", Aliases: []string{"x"}, Run: emptyRun}
	// Register out of order to prove the report follows registration order.
	root.AddCommand(c3, c1, c2)

	conflicts := root.CheckAliases()
	if len(conflicts) != 3 {
		t.Fatalf("expected 3 conflicts, got %d: %v", len(conflicts), conflicts)
	}
	want := [][2]*Command{
		{c3, c1},
		{c3, c2},
		{c1, c2},
	}
	for i, pair := range want {
		if conflicts[i].First.Command != pair[0] || conflicts[i].Second.Command != pair[1] {
			t.Errorf("conflict %d: expected %q vs %q, got %q vs %q", i,
				pair[0].CommandPath(), pair[1].CommandPath(),
				conflicts[i].First.Command.CommandPath(), conflicts[i].Second.Command.CommandPath())
		}
	}
}

// TestAliasCheckerNested verifies that the check recurses into nested
// command scopes.
func TestAliasCheckerNested(t *testing.T) {
	root := &Command{Use: "root"}
	branch := &Command{Use: "branch", Run: emptyRun}
	a1 := &Command{Use: "a1", Aliases: []string{"z"}, Run: emptyRun}
	a2 := &Command{Use: "a2", Aliases: []string{"z"}, Run: emptyRun}
	branch.AddCommand(a1, a2)
	root.AddCommand(branch)

	conflicts := root.CheckAliases()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict in nested scope, got %d: %v", len(conflicts), conflicts)
	}
	c := conflicts[0]
	if c.Parent != branch {
		t.Errorf("expected parent %q, got %q", branch.CommandPath(), c.Parent.CommandPath())
	}
	if c.Token != "z" || c.First.Command != a1 || c.Second.Command != a2 {
		t.Errorf("unexpected nested conflict: %+v", c)
	}
}

// TestAliasCheckerNoConflicts verifies that a clean tree reports nothing.
func TestAliasCheckerNoConflicts(t *testing.T) {
	root := &Command{Use: "root"}
	root.AddCommand(
		&Command{Use: "alpha", Aliases: []string{"a"}, Run: emptyRun},
		&Command{Use: "beta", Aliases: []string{"b"}, Run: emptyRun},
	)
	if conflicts := root.CheckAliases(); len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d: %v", len(conflicts), conflicts)
	}
}

// TestAliasCheckerReadOnly verifies that checking aliases does not execute
// commands, does not call Find (which would trigger argument parsing and set
// commandCalledAs), and does not modify the command tree.
func TestAliasCheckerReadOnly(t *testing.T) {
	root := &Command{Use: "root"}
	sync := &Command{Use: "sync", Aliases: []string{"s"}, Run: func(*Command, []string) {
		t.Fatal("command must not be executed during alias checking")
	}}
	realS := &Command{Use: "s", Run: func(*Command, []string) {
		t.Fatal("command must not be executed during alias checking")
	}}
	root.AddCommand(sync, realS)

	aliasesBefore := append([]string(nil), sync.Aliases...)
	suggestionsBefore := append([]string(nil), sync.SuggestFor...)
	commandsBefore := len(root.commands)
	parentBefore := sync.parent

	conflicts := root.CheckAliases()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	if !reflect.DeepEqual(sync.Aliases, aliasesBefore) {
		t.Errorf("Aliases were modified: before %v, after %v", aliasesBefore, sync.Aliases)
	}
	if !reflect.DeepEqual(sync.SuggestFor, suggestionsBefore) {
		t.Errorf("SuggestFor was modified: before %v, after %v", suggestionsBefore, sync.SuggestFor)
	}
	if len(root.commands) != commandsBefore {
		t.Errorf("number of children changed: before %d, after %d", commandsBefore, len(root.commands))
	}
	if sync.parent != parentBefore {
		t.Errorf("parent pointer changed")
	}
	if sync.CalledAs() != "" || realS.CalledAs() != "" {
		t.Errorf("commandCalledAs was set; Find must not be called by the checker")
	}
}

// TestAliasCheckerCaseInsensitive verifies that the checker respects the
// EnableCaseInsensitive global setting.
func TestAliasCheckerCaseInsensitive(t *testing.T) {
	old := EnableCaseInsensitive
	EnableCaseInsensitive = true
	defer func() { EnableCaseInsensitive = old }()

	root := &Command{Use: "root"}
	a := &Command{Use: "Alpha", Run: emptyRun}
	b := &Command{Use: "alpha", Run: emptyRun}
	root.AddCommand(a, b)

	conflicts := root.CheckAliases()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 case-insensitive conflict, got %d: %v", len(conflicts), conflicts)
	}
}
