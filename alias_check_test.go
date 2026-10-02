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
	"reflect"
	"strings"
	"testing"
)

// panicRun fails the test if the command is ever executed; the alias
// checker must be fully static.
func panicRun(t *testing.T) func(*Command, []string) {
	return func(*Command, []string) {
		t.Fatal("alias checker must not execute commands")
	}
}

func conflictSummary(cs []AliasConflict) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, string(c.Kind)+"|"+c.Token+"|"+c.FirstPath+"|"+c.SecondPath)
	}
	return out
}

func TestCheckAliasConflictsAliasVsName(t *testing.T) {
	root := &Command{Use: "root", Run: panicRun(t)}
	sync := &Command{Use: "sync", Aliases: []string{"s"}, Run: panicRun(t)}
	status := &Command{Use: "s", Run: panicRun(t)}
	tools := &Command{Use: "tools", Run: panicRun(t)}
	toolsS := &Command{Use: "s", Run: panicRun(t)}
	tools.AddCommand(toolsS)
	root.AddCommand(sync, status, tools)

	got := CheckAliasConflicts(root)

	// "s" under tools lives in a different parent scope and must not
	// conflict with the top-level "s" name or the sync alias.
	want := []string{
		"alias-vs-name|s|root sync|root s",
	}
	if diff := reflect.DeepEqual(want, conflictSummary(got)); !diff {
		t.Fatalf("unexpected conflicts:\nwant: %v\ngot:  %v", want, conflictSummary(got))
	}

	c := got[0]
	if c.First != sync || c.Second != status {
		t.Fatalf("expected conflict between sync and s commands, got %v and %v", c.First, c.Second)
	}
	// Both full paths must be printed.
	str := c.String()
	for _, wantSub := range []string{"alias-vs-name", `"s"`, `"root sync"`, `"root s"`} {
		if !strings.Contains(str, wantSub) {
			t.Errorf("conflict string %q missing %q", str, wantSub)
		}
	}
	report := AliasConflictReport(got)
	if !strings.Contains(report, "root sync") || !strings.Contains(report, "root s") {
		t.Errorf("report missing full paths:\n%s", report)
	}
}

func TestCheckAliasConflictsDuplicateDeclaration(t *testing.T) {
	root := &Command{Use: "root", Run: panicRun(t)}
	// Same alias declared twice on one command, plus an alias repeating
	// the command's own name: each gets a separate diagnostic.
	dup := &Command{Use: "dup", Aliases: []string{"d", "d", "dup"}, Run: panicRun(t)}
	root.AddCommand(dup)

	got := CheckAliasConflicts(root)
	want := []string{
		"duplicate-declaration|d|root dup|root dup",
		"duplicate-declaration|dup|root dup|root dup",
	}
	if !reflect.DeepEqual(want, conflictSummary(got)) {
		t.Fatalf("unexpected conflicts:\nwant: %v\ngot:  %v", want, conflictSummary(got))
	}
	for _, c := range got {
		if c.First != dup || c.Second != dup {
			t.Fatalf("duplicate declaration must reference the same command twice, got %v and %v", c.First, c.Second)
		}
	}
}

func TestCheckAliasConflictsKinds(t *testing.T) {
	root := &Command{Use: "root", Run: panicRun(t)}
	a := &Command{Use: "alpha", Aliases: []string{"shared"}, Run: panicRun(t)}
	b := &Command{Use: "beta", Aliases: []string{"shared"}, Run: panicRun(t)}
	c := &Command{Use: "beta", Run: panicRun(t)}
	d := &Command{Use: "delta", SuggestFor: []string{"alpha"}, Run: panicRun(t)}
	e := &Command{Use: "epsilon", SuggestFor: []string{"shared"}, Run: panicRun(t)}
	f := &Command{Use: "zeta", SuggestFor: []string{"hint"}, Run: panicRun(t)}
	g := &Command{Use: "eta", SuggestFor: []string{"hint"}, Run: panicRun(t)}
	root.AddCommand(a, b, c, d, e, f, g)

	got := CheckAliasConflicts(root)
	want := []string{
		// Registration order: alpha, beta(alias), beta(name), delta, epsilon, zeta, eta.
		"alias-vs-alias|shared|root alpha|root beta",
		"name-vs-name|beta|root beta|root beta",
		"suggest-vs-name|alpha|root alpha|root delta",
		"suggest-vs-alias|shared|root alpha|root epsilon",
		"suggest-vs-alias|shared|root beta|root epsilon",
		"suggest-vs-suggest|hint|root zeta|root eta",
	}
	if !reflect.DeepEqual(want, conflictSummary(got)) {
		t.Fatalf("unexpected conflicts:\nwant: %v\ngot:  %v", want, conflictSummary(got))
	}
}

func TestCheckAliasConflictsNestedAndHidden(t *testing.T) {
	root := &Command{Use: "root", Run: panicRun(t)}
	parent := &Command{Use: "parent", Run: panicRun(t)}
	// Hidden commands still participate in the check.
	hidden := &Command{Use: "secret", Hidden: true, Aliases: []string{"x"}, Run: panicRun(t)}
	other := &Command{Use: "x", Run: panicRun(t)}
	parent.AddCommand(hidden, other)
	root.AddCommand(parent)

	got := CheckAliasConflicts(root)
	want := []string{
		"alias-vs-name|x|root parent secret|root parent x",
	}
	if !reflect.DeepEqual(want, conflictSummary(got)) {
		t.Fatalf("unexpected conflicts:\nwant: %v\ngot:  %v", want, conflictSummary(got))
	}
}

func TestCheckAliasConflictsRegistrationOrder(t *testing.T) {
	build := func(t *testing.T) *Command {
		root := &Command{Use: "root", Run: panicRun(t)}
		// Deliberately registered in non-alphabetical order; the report
		// must follow this registration order, not sorted name order.
		z := &Command{Use: "zulu", Aliases: []string{"dup"}, Run: panicRun(t)}
		m := &Command{Use: "mike", Aliases: []string{"dup"}, Run: panicRun(t)}
		a := &Command{Use: "alpha", Aliases: []string{"zulu"}, Run: panicRun(t)}
		root.AddCommand(z, m, a)
		return root
	}

	want := []string{
		"alias-vs-alias|dup|root zulu|root mike",
		"alias-vs-name|zulu|root zulu|root alpha",
	}
	first := conflictSummary(CheckAliasConflicts(build(t)))
	if !reflect.DeepEqual(want, first) {
		t.Fatalf("unexpected conflicts:\nwant: %v\ngot:  %v", want, first)
	}
	// The report must be deterministic across repeated runs.
	for i := 0; i < 5; i++ {
		if got := conflictSummary(CheckAliasConflicts(build(t))); !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d: report order not deterministic:\nfirst: %v\ngot:   %v", i, first, got)
		}
	}
}

func TestCheckAliasConflictsReadOnly(t *testing.T) {
	root := &Command{Use: "root", Run: panicRun(t)}
	sync := &Command{Use: "sync", Aliases: []string{"s"}, SuggestFor: []string{"synch"}, Run: panicRun(t)}
	status := &Command{Use: "s", Run: panicRun(t)}
	tools := &Command{Use: "tools", Run: panicRun(t)}
	toolsS := &Command{Use: "s", Aliases: []string{"s"}, Run: panicRun(t)}
	tools.AddCommand(toolsS)
	root.AddCommand(sync, status, tools)

	// Snapshot everything the checker is not allowed to touch.
	aliasesBefore := append([]string(nil), sync.Aliases...)
	suggestBefore := append([]string(nil), sync.SuggestFor...)
	orderBefore := append([]*Command(nil), root.commands...)
	toolsOrderBefore := append([]*Command(nil), tools.commands...)
	sortedBefore := root.commandsAreSorted

	got := CheckAliasConflicts(root)
	if len(got) == 0 {
		t.Fatal("expected conflicts to be reported")
	}

	if !reflect.DeepEqual(aliasesBefore, sync.Aliases) {
		t.Errorf("Aliases modified: before %v, after %v", aliasesBefore, sync.Aliases)
	}
	if !reflect.DeepEqual(suggestBefore, sync.SuggestFor) {
		t.Errorf("SuggestFor modified: before %v, after %v", suggestBefore, sync.SuggestFor)
	}
	if !reflect.DeepEqual(orderBefore, root.commands) {
		t.Errorf("child registration order modified: before %v, after %v", orderBefore, root.commands)
	}
	if !reflect.DeepEqual(toolsOrderBefore, tools.commands) {
		t.Errorf("nested child registration order modified")
	}
	if root.commandsAreSorted != sortedBefore {
		t.Errorf("commandsAreSorted flipped: checker must not sort the command tree")
	}
	if sync.CalledAs() != "" || status.CalledAs() != "" || toolsS.CalledAs() != "" {
		t.Errorf("called-as state changed: checker must not resolve or execute commands")
	}
}

func TestCheckAliasConflictsMethodAndEmpty(t *testing.T) {
	if got := CheckAliasConflicts(nil); got != nil {
		t.Errorf("nil root: expected nil, got %v", got)
	}
	clean := &Command{Use: "root", Run: panicRun(t)}
	child := &Command{Use: "child", Aliases: []string{"c"}, Run: panicRun(t)}
	clean.AddCommand(child)
	if got := clean.CheckAliasConflicts(); len(got) != 0 {
		t.Errorf("clean tree: expected no conflicts, got %v", conflictSummary(got))
	}
	if report := AliasConflictReport(nil); report != "" {
		t.Errorf("empty report: expected empty string, got %q", report)
	}
}
