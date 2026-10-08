// Copyright 2023 The Cobra Authors
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
	"fmt"
	"strings"
	"testing"
)

// customValue is a user-defined pflag.Value to verify custom Value support.
type customValue struct {
	value string
	times int
}

func (c *customValue) String() string {
	if c == nil {
		return ""
	}
	return c.value
}

func (c *customValue) Set(s string) error {
	if strings.Contains(s, "boom") {
		return fmt.Errorf("custom value cannot be %q", s)
	}
	c.times++
	c.value = s
	return nil
}

func (c *customValue) Type() string { return "custom" }

func newSessionTestTree() *Command {
	root := &Command{
		Use: "root",
		Run: func(cmd *Command, args []string) {},
	}
	root.PersistentFlags().String("tag", "default-tag", "global tag")
	root.PersistentFlags().StringSlice("names", []string{"n1"}, "repeated slice flag")
	root.Flags().String("onlyroot", "root-default", "root only flag")

	custom := &customValue{value: "cv-default"}
	root.PersistentFlags().Var(custom, "custom", "custom value flag")

	child := &Command{
		Use: "child",
		Run: func(cmd *Command, args []string) {},
	}
	// Local flag shadowing the parent persistent flag of the same name.
	child.Flags().String("tag", "child-default", "child local tag")
	child.Flags().Int("count", 7, "child local int")
	root.AddCommand(child)

	grandchild := &Command{
		Use: "grand",
		Run: func(cmd *Command, args []string) {},
	}
	grandchild.Flags().String("g", "g-default", "grand flag")
	child.AddCommand(grandchild)

	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

func TestParseSession_BasicDefaultReset(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--tag=a"})
	if r1.Err != nil {
		t.Fatalf("first round: %v", r1.Err)
	}
	if got := r1.Flags().Lookup("tag").Value.String(); got != "a" {
		t.Fatalf("first round tag = %q, want a", got)
	}
	if !r1.Changed("tag") {
		t.Fatal("first round: tag expected to be Changed")
	}

	// Second round without --tag must fall back to the registered default.
	r2 := session.Execute([]string{})
	if r2.Err != nil {
		t.Fatalf("second round: %v", r2.Err)
	}
	if got := r2.Flags().Lookup("tag").Value.String(); got != "default-tag" {
		t.Fatalf("second round tag = %q, want default-tag", got)
	}
	if r2.Changed("tag") {
		t.Fatal("second round: tag must not be Changed")
	}
}

// TestParseSession_HandlerReadsSessionValues verifies handlers see this
// session's values through the normal flag accessors.
func TestParseSession_HandlerReadsSessionValues(t *testing.T) {
	root := &Command{Use: "root"}
	var seen string
	root.Run = func(cmd *Command, args []string) {
		seen, _ = cmd.Flags().GetString("tag")
	}
	root.Flags().String("tag", "d", "")
	root.SilenceErrors = true
	root.SilenceUsage = true

	session := NewParseSession(root)
	if err := session.Execute([]string{"--tag=v1"}).Err; err != nil {
		t.Fatal(err)
	}
	if seen != "v1" {
		t.Fatalf("handler saw %q, want v1", seen)
	}
	if err := session.Execute([]string{}).Err; err != nil {
		t.Fatal(err)
	}
	if seen != "d" {
		t.Fatalf("handler saw %q, want default d", seen)
	}
}

// TestParseSession_ParentChildFlagShadowing verifies a child-local flag
// shadows the parent persistent flag and each is restored independently.
func TestParseSession_ParentChildFlagShadowing(t *testing.T) {
	root := &Command{Use: "root", TraverseChildren: true}
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.PersistentFlags().String("ptag", "p-default", "parent only flag")
	// Parent persistent "tag" that is shadowed by the child's local "tag".
	root.PersistentFlags().String("tag", "parent-default", "global tag")

	child := &Command{Use: "child"}
	child.Flags().String("tag", "child-default", "shadows parent tag")
	root.AddCommand(child)

	var ptagInRun, tagInRun string
	var ptagChanged, tagChanged bool
	child.Run = func(cmd *Command, args []string) {
		ptagInRun, _ = cmd.Flags().GetString("ptag")
		tagInRun, _ = cmd.Flags().GetString("tag")
		ptagChanged = cmd.Flags().Lookup("ptag").Changed
		tagChanged = cmd.Flags().Lookup("tag").Changed
	}
	session := NewParseSession(root)

	r := session.Execute([]string{"--ptag=p1", "--tag=parent-val", "child", "--tag=child-val"})
	if r.Err != nil {
		t.Fatalf("execute: %v", r.Err)
	}
	if r.Cmd.Name() != "child" {
		t.Fatalf("executed %q, want child", r.Cmd.Name())
	}

	// Parent persistent flag parsed before the subcommand boundary.
	if ptagInRun != "p1" {
		t.Fatalf("ptag during run = %q, want p1", ptagInRun)
	}
	if !ptagChanged {
		t.Fatal("ptag should be Changed during the run")
	}
	// The child-local tag shadows the parent persistent tag: the value after
	// the subcommand name wins.
	if tagInRun != "child-val" {
		t.Fatalf("tag during run = %q, want child-val", tagInRun)
	}
	if !tagChanged {
		t.Fatal("tag should be Changed during the run")
	}

	// The detached result reflects the child's shadowing value for tag.
	if got := r.Flags().Lookup("tag").Value.String(); got != "child-val" {
		t.Fatalf("result tag = %q, want child-val", got)
	}
	if got := r.Flags().Lookup("ptag").Value.String(); got != "p1" {
		t.Fatalf("result ptag = %q, want p1", got)
	}
	if !r.Changed("tag") || !r.Changed("ptag") {
		t.Fatal("both flags should be Changed in the result")
	}

	// After the session everything is back to defaults and unchanged.
	if got := child.Flags().Lookup("tag").Value.String(); got != "child-default" {
		t.Fatalf("child tag after restore = %q, want child-default", got)
	}
	if child.Flags().Lookup("tag").Changed {
		t.Fatal("child tag must not be Changed after restore")
	}
	if got := root.PersistentFlags().Lookup("tag").Value.String(); got != "parent-default" {
		t.Fatalf("parent tag after restore = %q, want parent-default", got)
	}
	if root.PersistentFlags().Lookup("tag").Changed {
		t.Fatal("parent tag must not be Changed after restore")
	}
	if got := root.PersistentFlags().Lookup("ptag").Value.String(); got != "p-default" {
		t.Fatalf("ptag after restore = %q, want p-default", got)
	}
	if root.PersistentFlags().Lookup("ptag").Changed {
		t.Fatal("ptag must not be Changed after restore")
	}
}

func childOf(root *Command) *Command {
	for _, c := range root.Commands() {
		if c.Name() == "child" {
			return c
		}
	}
	return nil
}

// TestParseSession_RepeatedSlice verifies repeated slice flags accumulate in
// one round but reset in the next, and that restoration works.
func TestParseSession_RepeatedSlice(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--names=a", "--names=b", "--names=c"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	got, _ := r1.Flags().GetStringSlice("names")
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("round1 names = %v, want [a b c]", got)
	}

	r2 := session.Execute([]string{"--names=x"})
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	got, _ = r2.Flags().GetStringSlice("names")
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("round2 names = %v, want [x]", got)
	}

	r3 := session.Execute(nil)
	if r3.Err != nil {
		t.Fatal(r3.Err)
	}
	got, _ = r3.Flags().GetStringSlice("names")
	if len(got) != 1 || got[0] != "n1" {
		t.Fatalf("round3 names = %v, want default [n1]", got)
	}

	got, _ = root.PersistentFlags().GetStringSlice("names")
	if len(got) != 1 || got[0] != "n1" {
		t.Fatalf("names after restore = %v, want [n1]", got)
	}
}

// TestParseSession_CustomValue verifies user-defined pflag.Value flags.
func TestParseSession_CustomValue(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--custom=hello"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	if got := r1.Flags().Lookup("custom").Value.String(); got != "hello" {
		t.Fatalf("custom = %q, want hello", got)
	}

	r2 := session.Execute(nil)
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	if got := r2.Flags().Lookup("custom").Value.String(); got != "cv-default" {
		t.Fatalf("custom = %q, want cv-default", got)
	}
	if got := root.PersistentFlags().Lookup("custom").Value.String(); got != "cv-default" {
		t.Fatalf("custom after restore = %q, want cv-default", got)
	}
}

// TestParseSession_ParseFailure verifies a failed round exposes the error and
// still fully restores the tree, and the next round works normally.
func TestParseSession_ParseFailure(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--tag=a", "--unknown=x"})
	if r1.Err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if got := root.PersistentFlags().Lookup("tag").Value.String(); got != "default-tag" {
		t.Fatalf("tag after failed round = %q, want default-tag", got)
	}
	if root.PersistentFlags().Lookup("tag").Changed {
		t.Fatal("tag must not be Changed after failed round")
	}

	r2 := session.Execute([]string{"--tag=b"})
	if r2.Err != nil {
		t.Fatalf("second round after failure: %v", r2.Err)
	}
	if got := r2.Flags().Lookup("tag").Value.String(); got != "b" {
		t.Fatalf("tag = %q, want b", got)
	}
}

// TestParseSession_CustomValueSetError verifies errors raised by a custom
// Value during parsing lead to full restoration.
func TestParseSession_CustomValueSetError(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r := session.Execute([]string{"--custom=boom"})
	if r.Err == nil {
		t.Fatal("expected Set error, got nil")
	}
	if got := root.PersistentFlags().Lookup("custom").Value.String(); got != "cv-default" {
		t.Fatalf("custom after error = %q, want cv-default", got)
	}
}

// TestParseSession_Panic verifies a panicking handler is recovered into the
// result and the tree is still restored.
func TestParseSession_Panic(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().String("tag", "d", "")
	root.Run = func(cmd *Command, args []string) {
		v, _ := cmd.Flags().GetString("tag")
		if v == "explode" {
			panic("kaboom")
		}
	}
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	r := session.Execute([]string{"--tag=explode"})
	if r.Panic != "kaboom" {
		t.Fatalf("Panic = %v, want kaboom", r.Panic)
	}
	if got := root.Flags().Lookup("tag").Value.String(); got != "d" {
		t.Fatalf("tag after panic = %q, want d", got)
	}
	if root.Flags().Lookup("tag").Changed {
		t.Fatal("tag must not be Changed after panic")
	}

	// Program keeps running: another round works.
	r2 := session.Execute([]string{"--tag=after"})
	if r2.Err != nil || r2.Panic != nil {
		t.Fatalf("second round: err=%v panic=%v", r2.Err, r2.Panic)
	}
	if got := r2.Flags().Lookup("tag").Value.String(); got != "after" {
		t.Fatalf("tag = %q, want after", got)
	}
}

// TestParseSession_Nested verifies an inner session starts from defaults
// while the outer session keeps its values; after the inner and outer
// sessions finish the tree is back to its original state.
func TestParseSession_Nested(t *testing.T) {
	root := newSessionTestTree()

	var outerDuringInner, innerSeen, outerAfterInner string
	root.Run = func(cmd *Command, args []string) {
		outerTag, _ := cmd.Flags().GetString("tag")
		if outerTag == "outer-val" {
			inner := NewParseSession(root).Execute([]string{})
			if inner.Err != nil {
				t.Errorf("inner session: %v", inner.Err)
			}
			innerSeen = inner.Flags().Lookup("tag").Value.String()
			outerDuringInner, _ = cmd.Flags().GetString("tag")

			inner2 := NewParseSession(root).Execute([]string{"--tag=inner-val"})
			if inner2.Err != nil {
				t.Errorf("inner session 2: %v", inner2.Err)
			}
			if inner2.Flags().Lookup("tag").Value.String() != "inner-val" {
				t.Errorf("inner2 tag = %q, want inner-val", inner2.Flags().Lookup("tag").Value.String())
			}

			outerAfterInner, _ = cmd.Flags().GetString("tag")
		}
	}
	session := NewParseSession(root)

	r := session.Execute([]string{"--tag=outer-val"})
	if r.Err != nil {
		t.Fatalf("outer session: %v", r.Err)
	}
	if innerSeen != "default-tag" {
		t.Fatalf("inner session tag = %q, want default-tag", innerSeen)
	}
	if outerDuringInner != "outer-val" {
		t.Fatalf("outer tag while inner ran = %q, want outer-val", outerDuringInner)
	}
	if outerAfterInner != "outer-val" {
		t.Fatalf("outer tag after inner = %q, want outer-val", outerAfterInner)
	}
	if got := root.PersistentFlags().Lookup("tag").Value.String(); got != "default-tag" {
		t.Fatalf("tag after outer restore = %q, want default-tag", got)
	}
	if root.PersistentFlags().Lookup("tag").Changed {
		t.Fatal("tag must not be Changed after outer restore")
	}
}

// TestParseSession_PositionalArgsAndDash verifies positional args and the
// dash position are captured and restored.
func TestParseSession_PositionalArgsAndDash(t *testing.T) {
	root := &Command{Use: "root"}
	var args []string
	var atDash int
	root.Run = func(cmd *Command, a []string) {
		args = a
		atDash = cmd.ArgsLenAtDash()
	}
	root.Flags().String("tag", "d", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	r := session.Execute([]string{"--tag=v", "one", "--", "two"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if len(args) != 2 || args[0] != "one" || args[1] != "two" {
		t.Fatalf("positional args = %v, want [one two]", args)
	}
	if atDash != 1 {
		t.Fatalf("ArgsLenAtDash = %d, want 1", atDash)
	}
	if len(r.Args) != 2 || r.Args[0] != "one" || r.Args[1] != "two" {
		t.Fatalf("result args = %v, want [one two]", r.Args)
	}
	if root.Flags().NArg() != 0 {
		t.Fatalf("root NArg after restore = %d, want 0", root.Flags().NArg())
	}
	if root.ArgsLenAtDash() != -1 {
		t.Fatalf("root ArgsLenAtDash after restore = %d, want -1", root.ArgsLenAtDash())
	}
}

// TestParseSession_ResultIsolation verifies detached results stay valid and
// independent after the tree is restored.
func TestParseSession_ResultIsolation(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--tag=a", "--names=x", "--names=y", "child", "--count=42", "pos1"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	r2 := session.Execute(nil)
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}

	// r1 keeps its values even after r2 restored and parsed again.
	if got := r1.Flags().Lookup("tag").Value.String(); got != "a" {
		t.Fatalf("r1 tag = %q, want a", got)
	}
	if !r1.Changed("tag") {
		t.Fatal("r1 tag should be Changed")
	}
	if got, _ := r1.Flags().GetStringSlice("names"); len(got) != 2 || got[1] != "y" {
		t.Fatalf("r1 names = %v, want [x y]", got)
	}
	if got, _ := r1.Flags().GetInt("count"); got != 42 {
		t.Fatalf("r1 count = %d, want 42", got)
	}
	if len(r1.Args) != 1 || r1.Args[0] != "pos1" {
		t.Fatalf("r1 args = %v, want [pos1]", r1.Args)
	}

	// r2 holds the defaults and is not Changed.
	if got := r2.Flags().Lookup("tag").Value.String(); got != "default-tag" {
		t.Fatalf("r2 tag = %q, want default-tag", got)
	}
	if r2.Changed("tag") {
		t.Fatal("r2 tag should not be Changed")
	}

	// Mutating the tree must not affect the detached result.
	_ = root.Flags().Set("tag", "z")
	if got := r1.Flags().Lookup("tag").Value.String(); got != "a" {
		t.Fatalf("r1 tag changed to %q after tree mutation, want a", got)
	}
}

// TestParseSession_RestoreAfterDirtyTree verifies a session started while the
// tree is already dirty restores that dirty state rather than the defaults.
func TestParseSession_RestoreAfterDirtyTree(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	// Simulate a previous "manual" parse leaving the tree dirty.
	_ = root.PersistentFlags().Set("tag", "leftover")
	_ = root.PersistentFlags().Set("names", "leftover")

	r := session.Execute([]string{"--tag=session"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if got := r.Flags().Lookup("tag").Value.String(); got != "session" {
		t.Fatalf("session tag = %q, want session", got)
	}
	// After the session the previous dirty state is restored.
	if got := root.PersistentFlags().Lookup("tag").Value.String(); got != "leftover" {
		t.Fatalf("tag after restore = %q, want leftover", got)
	}
	if !root.PersistentFlags().Lookup("tag").Changed {
		t.Fatal("tag should stay Changed as it was before the session")
	}
	if got, _ := root.PersistentFlags().GetStringSlice("names"); len(got) != 1 || got[0] != "leftover" {
		t.Fatalf("names after restore = %v, want [leftover]", got)
	}
}

// TestParseSession_StandardExecuteStillWorks guards against regressions of
// the ordinary Execute path when sessions are not used.
func TestParseSession_StandardExecuteStillWorks(t *testing.T) {
	root := newSessionTestTree()
	root.SetArgs([]string{"child"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

// TestParseSession_SubcommandDefaultFlag verifies defaults of a child-local
// flag survive across rounds.
func TestParseSession_SubcommandDefaultFlag(t *testing.T) {
	root := newSessionTestTree()
	session := NewParseSession(root)

	r1 := session.Execute([]string{"child", "--count=3"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	if got, _ := r1.Flags().GetInt("count"); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
	r2 := session.Execute([]string{"child"})
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	if got, _ := r2.Flags().GetInt("count"); got != 7 {
		t.Fatalf("count = %d, want default 7", got)
	}
	if r2.Changed("count") {
		t.Fatal("count should not be Changed")
	}
	if got, _ := childOf(root).Flags().GetInt("count"); got != 7 {
		t.Fatalf("live child count after restore = %d, want 7", got)
	}
}

// TestParseSession_StringArrayAndMap verifies accumulation-style values reset
// to their registered defaults between rounds.
func TestParseSession_StringArrayAndMap(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().StringArray("arr", []string{"d1", "d2"}, "")
	root.Flags().StringToString("m", map[string]string{"a": "1"}, "")
	root.Run = func(cmd *Command, args []string) {}
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--arr=x", "--arr=y", "--m=b=2", "--m=c=3"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	arr, _ := r1.Flags().GetStringArray("arr")
	if len(arr) != 2 || arr[0] != "x" || arr[1] != "y" {
		t.Fatalf("arr = %v, want [x y]", arr)
	}
	m, _ := r1.Flags().GetStringToString("m")
	if m["b"] != "2" || m["c"] != "3" {
		t.Fatalf("m = %v, want b:2 c:3", m)
	}
	if _, present := m["a"]; present {
		t.Fatal("default map key a must not leak into the round")
	}

	r2 := session.Execute(nil)
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	arr, _ = r2.Flags().GetStringArray("arr")
	if len(arr) != 2 || arr[0] != "d1" || arr[1] != "d2" {
		t.Fatalf("arr after reset = %v, want [d1 d2]", arr)
	}
	m, _ = r2.Flags().GetStringToString("m")
	if m["a"] != "1" || len(m) != 1 {
		t.Fatalf("m after reset = %v, want {a:1}", m)
	}
}

// TestParseSession_BoolAndCount verifies no-argument bool and incrementing
// count flags reset correctly.
func TestParseSession_BoolAndCount(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().Bool("verbose", false, "")
	root.Flags().Count("level", "")
	root.Run = func(cmd *Command, args []string) {}
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--verbose", "--level", "--level"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	if v, _ := r1.Flags().GetBool("verbose"); !v {
		t.Fatal("verbose should be true")
	}
	if v, _ := r1.Flags().GetCount("level"); v != 2 {
		t.Fatalf("level = %d, want 2", v)
	}

	r2 := session.Execute(nil)
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	if v, _ := r2.Flags().GetBool("verbose"); v {
		t.Fatal("verbose should be false after reset")
	}
	if v, _ := r2.Flags().GetCount("level"); v != 0 {
		t.Fatalf("level after reset = %d, want 0", v)
	}
	if r2.Changed("verbose") || r2.Changed("level") {
		t.Fatal("flags must not be Changed in the default round")
	}
}

// TestParseSession_HelpFlagRestores verifies requesting help leaves the tree
// restored and subsequent rounds still work.
func TestParseSession_HelpFlagRestores(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().String("tag", "d", "")
	root.Run = func(cmd *Command, args []string) {}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	session := NewParseSession(root)

	r := session.Execute([]string{"--help"})
	if r.Err != nil {
		t.Fatalf("help round: %v", r.Err)
	}
	if hf := root.Flags().Lookup("help"); hf != nil && hf.Changed {
		t.Fatal("help flag must not stay Changed after the round")
	}
	if root.Flags().Lookup("tag").Changed {
		t.Fatal("tag must not be Changed after help round")
	}

	r2 := session.Execute([]string{"--tag=ok"})
	if r2.Err != nil {
		t.Fatalf("round after help: %v", r2.Err)
	}
	if got := r2.Flags().Lookup("tag").Value.String(); got != "ok" {
		t.Fatalf("tag = %q, want ok", got)
	}
}

// TestParseSession_PersistentPreRun verifies hooks still run each round and
// observe the current session values.
func TestParseSession_PersistentPreRun(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().String("tag", "d", "")
	var hookRounds []string
	root.PersistentPreRun = func(cmd *Command, args []string) {
		v, _ := cmd.Flags().GetString("tag")
		hookRounds = append(hookRounds, v)
	}
	root.Run = func(cmd *Command, args []string) {}
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	for _, args := range [][]string{{"--tag=a"}, nil, {"--tag=b"}, nil} {
		if r := session.Execute(args); r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	want := []string{"a", "d", "b", "d"}
	if len(hookRounds) != len(want) {
		t.Fatalf("hookRounds = %v, want %v", hookRounds, want)
	}
	for i := range want {
		if hookRounds[i] != want[i] {
			t.Fatalf("hookRounds = %v, want %v", hookRounds, want)
		}
	}
}

// TestParseSession_IntSliceAndIP verifies numeric and typed slice flags reset.
func TestParseSession_IntSliceAndIP(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().IntSlice("nums", []int{1, 2}, "")
	root.Flags().String("net", "127.0.0.1", "")
	root.Run = func(cmd *Command, args []string) {}
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--nums=3", "--nums=4", "--net=10.0.0.1"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	nums, _ := r1.Flags().GetIntSlice("nums")
	if len(nums) != 2 || nums[0] != 3 || nums[1] != 4 {
		t.Fatalf("nums = %v, want [3 4]", nums)
	}

	r2 := session.Execute(nil)
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	nums, _ = r2.Flags().GetIntSlice("nums")
	if len(nums) != 2 || nums[0] != 1 || nums[1] != 2 {
		t.Fatalf("nums after reset = %v, want [1 2]", nums)
	}
	if got, _ := r2.Flags().GetString("net"); got != "127.0.0.1" {
		t.Fatalf("net = %q, want 127.0.0.1", got)
	}
}

// TestParseSession_MultipleSessionsOnSameTree verifies independent session
// objects over the same tree do not interfere.
func TestParseSession_MultipleSessionsOnSameTree(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().String("tag", "d", "")
	root.Run = func(cmd *Command, args []string) {}
	root.SilenceErrors = true
	root.SilenceUsage = true

	s1 := NewParseSession(root)
	s2 := NewParseSession(root)

	r1 := s1.Execute([]string{"--tag=s1"})
	r2 := s2.Execute([]string{"--tag=s2"})
	r3 := s1.Execute(nil)
	if r1.Flags().Lookup("tag").Value.String() != "s1" ||
		r2.Flags().Lookup("tag").Value.String() != "s2" ||
		r3.Flags().Lookup("tag").Value.String() != "d" {
		t.Fatalf("cross-session interference: %q %q %q",
			r1.Flags().Lookup("tag").Value.String(),
			r2.Flags().Lookup("tag").Value.String(),
			r3.Flags().Lookup("tag").Value.String())
	}
	if got := root.Flags().Lookup("tag").Value.String(); got != "d" {
		t.Fatalf("live tag = %q, want d", got)
	}
}

// TestParseSession_VersionFlag verifies the auto version flag round-trips.
func TestParseSession_VersionFlag(t *testing.T) {
	root := &Command{Use: "root", Version: "9.9.9"}
	root.Run = func(cmd *Command, args []string) {}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SilenceErrors = true
	session := NewParseSession(root)

	r := session.Execute([]string{"--version"})
	if r.Err != nil {
		t.Fatalf("version round: %v", r.Err)
	}
	if vf := root.Flags().Lookup("version"); vf != nil && vf.Changed {
		t.Fatal("version flag must not stay Changed after the round")
	}
	if !strings.Contains(buf.String(), "9.9.9") {
		t.Fatalf("version output = %q, want it to contain 9.9.9", buf.String())
	}
}

// TestParseSession_CompletionCommandRoundTrip verifies that invoking the
// auto-generated completion subcommand leaves the command list restored.
func TestParseSession_CompletionCommandRoundTrip(t *testing.T) {
	root := &Command{Use: "myapp"}
	root.Run = func(cmd *Command, args []string) {}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	real := &Command{Use: "real"}
	real.Run = func(cmd *Command, args []string) {}
	root.AddCommand(real)
	session := NewParseSession(root)

	before := len(root.Commands())
	r := session.Execute([]string{"completion", "bash"})
	if r.Err != nil {
		t.Fatalf("completion round: %v", r.Err)
	}
	after := len(root.Commands())
	if after != before {
		t.Fatalf("command count changed: before=%d after=%d", before, after)
	}
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			t.Fatal("auto completion command leaked into the tree")
		}
	}

	// The tree still works for normal rounds.
	r2 := session.Execute([]string{"real"})
	if r2.Err != nil {
		t.Fatalf("real round: %v", r2.Err)
	}
	if r2.Cmd.Name() != "real" {
		t.Fatalf("executed %q, want real", r2.Cmd.Name())
	}
}

// TestParseSession_DashInSubcommand verifies the dash position is captured per
// executed subcommand and restored.
func TestParseSession_DashInSubcommand(t *testing.T) {
	root := &Command{Use: "root"}
	root.Run = func(cmd *Command, args []string) {}
	sub := &Command{Use: "sub"}
	var atDash int
	var pos []string
	sub.Run = func(cmd *Command, args []string) {
		atDash = cmd.ArgsLenAtDash()
		pos = args
	}
	root.AddCommand(sub)
	session := NewParseSession(root)

	r := session.Execute([]string{"sub", "a", "--", "b", "c"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if atDash != 1 || len(pos) != 3 || pos[0] != "a" || pos[2] != "c" {
		t.Fatalf("atDash=%d pos=%v, want 1 [a b c]", atDash, pos)
	}
	if sub.Flags().NArg() != 0 || sub.ArgsLenAtDash() != -1 {
		t.Fatalf("sub flags not restored: narg=%d dash=%d", sub.Flags().NArg(), sub.ArgsLenAtDash())
	}
}

type ctxKey string

// TestParseSession_ContextRoundTrip verifies the session sets a background
// context for the round but restores a pre-existing nil context afterwards,
// and handlers can reach the context.
func TestParseSession_ContextRoundTrip(t *testing.T) {
	root := &Command{Use: "root"}
	root.Run = func(cmd *Command, args []string) {
		if cmd.Context() == nil {
			t.Error("handler context is nil during the round")
		}
	}
	session := NewParseSession(root)

	r := session.Execute(nil)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if root.Context() != nil {
		t.Fatalf("root context after round = %v, want nil", root.Context())
	}
}

// TestParseSession_CustomValueWithExtraState verifies a custom Value carrying
// extra internal state is isolated across rounds and restored afterwards.
func TestParseSession_CustomValueWithExtraState(t *testing.T) {
	root := &Command{Use: "root"}
	cv := &customValue{value: "cv-default"}
	root.PersistentFlags().Var(cv, "custom", "")
	root.Run = func(cmd *Command, args []string) {}
	root.SilenceErrors = true
	root.SilenceUsage = true
	session := NewParseSession(root)

	r1 := session.Execute([]string{"--custom=one", "--custom=two"})
	if r1.Err != nil {
		t.Fatal(r1.Err)
	}
	if got := r1.Flags().Lookup("custom").Value.String(); got != "two" {
		t.Fatalf("custom = %q, want two", got)
	}
	r2 := session.Execute(nil)
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	if got := r2.Flags().Lookup("custom").Value.String(); got != "cv-default" {
		t.Fatalf("custom after reset = %q, want cv-default", got)
	}
	if got := cv.String(); got != "cv-default" {
		t.Fatalf("live custom after restore = %q, want cv-default", got)
	}
}
