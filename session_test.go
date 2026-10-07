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
	"errors"
	"reflect"
	"strings"
	"testing"
)

// upperValue is a custom pflag.Value used to verify sessions work with
// user-defined value types.
type upperValue struct {
	v string
}

func (u *upperValue) String() string { return u.v }
func (u *upperValue) Set(s string) error {
	u.v = strings.ToUpper(s)
	return nil
}
func (u *upperValue) Type() string { return "upperValue" }

func mustGetString(t *testing.T, c *Command, name string) string {
	t.Helper()
	got, err := c.Flags().GetString(name)
	if err != nil {
		t.Fatalf("GetString(%q) failed: %v", name, err)
	}
	return got
}

func TestParseSessionResetsToDefaults(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().String("tag", "default", "tag usage")

	// First session sets the flag explicitly.
	s1 := c.NewParseSession()
	res1, err := s1.Parse([]string{"--tag=a", "pos1"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got := mustGetString(t, c, "tag"); got != "a" {
		t.Errorf("During session, cmd flag should report session value, got %q", got)
	}
	if got, _ := res1.Flags().GetString("tag"); got != "a" {
		t.Errorf("Result should hold session value, got %q", got)
	}
	if !res1.Changed("tag") {
		t.Error("Result should report tag as changed")
	}
	if !reflect.DeepEqual(res1.Args(), []string{"pos1"}) {
		t.Errorf("Expected positional args [pos1], got %v", res1.Args())
	}
	s1.End()

	// After the session everything is back to the registered state.
	if got := mustGetString(t, c, "tag"); got != "default" {
		t.Errorf("After End, flag should be restored to default, got %q", got)
	}
	if c.Flags().Changed("tag") {
		t.Error("After End, Changed should be restored to false")
	}

	// A second session without the flag must see the default, not "a".
	s2 := c.NewParseSession()
	res2, err := s2.Parse(nil)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := res2.Flags().GetString("tag"); got != "default" {
		t.Errorf("Second session should start from default, got %q", got)
	}
	if res2.Changed("tag") {
		t.Error("Second session should not report tag as changed")
	}
	s2.End()

	// The first result is isolated and still holds its own values.
	if got, _ := res1.Flags().GetString("tag"); got != "a" {
		t.Errorf("First result should be isolated, got %q", got)
	}
	if got := mustGetString(t, c, "tag"); got != "default" {
		t.Errorf("After second End, flag should be restored to default, got %q", got)
	}
}

func TestParseSessionParentChildFlagShadowing(t *testing.T) {
	parent := &Command{Use: "parent"}
	child := &Command{Use: "child"}
	parent.AddCommand(child)

	parent.PersistentFlags().String("tag", "parent-default", "parent tag")
	parent.PersistentFlags().String("other", "other-default", "parent other")
	child.Flags().String("tag", "child-default", "child tag shadows parent")

	s := child.NewParseSession()
	res, err := s.Parse([]string{"--tag=x", "--other=y"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}

	// The local flag of the child shadows the persistent one of the parent.
	if got, _ := res.Flags().GetString("tag"); got != "x" {
		t.Errorf("Child flag should be parsed, got %q", got)
	}
	if got, _ := res.Flags().GetString("other"); got != "y" {
		t.Errorf("Inherited flag should be parsed, got %q", got)
	}
	// The shadowed parent flag object is untouched.
	if got := parent.PersistentFlags().Lookup("tag").Value.String(); got != "parent-default" {
		t.Errorf("Shadowed parent flag should be untouched, got %q", got)
	}
	if parent.PersistentFlags().Changed("tag") {
		t.Error("Shadowed parent flag should not be marked changed")
	}
	s.End()

	if got := child.Flags().Lookup("tag").Value.String(); got != "child-default" {
		t.Errorf("Child flag should be restored, got %q", got)
	}
	if child.Flags().Changed("tag") || child.Flags().Changed("other") {
		t.Error("Changed states should be restored after End")
	}

	// A session on the parent still sees its own persistent flag.
	ps := parent.NewParseSession()
	pres, err := ps.Parse([]string{"--tag=z"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := pres.Flags().GetString("tag"); got != "z" {
		t.Errorf("Parent session should parse parent flag, got %q", got)
	}
	ps.End()
	if got := parent.PersistentFlags().Lookup("tag").Value.String(); got != "parent-default" {
		t.Errorf("Parent flag should be restored, got %q", got)
	}
}

func TestParseSessionRepeatedSliceFlags(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().StringSlice("item", []string{"d0"}, "item usage")

	s1 := c.NewParseSession()
	res1, err := s1.Parse([]string{"--item=a", "--item=b"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	got, err := res1.Flags().GetStringSlice("item")
	if err != nil {
		t.Fatalf("GetStringSlice failed: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Repeated slice args should replace the default, got %v", got)
	}
	s1.End()

	restored, err := c.Flags().GetStringSlice("item")
	if err != nil {
		t.Fatalf("GetStringSlice failed: %v", err)
	}
	if !reflect.DeepEqual(restored, []string{"d0"}) {
		t.Errorf("Slice flag should be restored to default, got %v", restored)
	}
	if c.Flags().Changed("item") {
		t.Error("Changed should be restored to false")
	}

	// The next session must replace (not append to) the default again.
	s2 := c.NewParseSession()
	res2, err := s2.Parse([]string{"--item=c"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	got, _ = res2.Flags().GetStringSlice("item")
	if !reflect.DeepEqual(got, []string{"c"}) {
		t.Errorf("Second session should replace default, got %v", got)
	}
	s2.End()

	// And a session without the flag sees the registered default.
	s3 := c.NewParseSession()
	res3, err := s3.Parse(nil)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	got, _ = res3.Flags().GetStringSlice("item")
	if !reflect.DeepEqual(got, []string{"d0"}) {
		t.Errorf("Third session should see default, got %v", got)
	}
	s3.End()

	// The first result remains isolated.
	got, _ = res1.Flags().GetStringSlice("item")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("First result should be isolated, got %v", got)
	}
}

func TestParseSessionCustomValue(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().Var(&upperValue{v: "def"}, "custom", "custom usage")

	s1 := c.NewParseSession()
	res1, err := s1.Parse([]string{"--custom=abc"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	uv, ok := res1.Lookup("custom").Value.(*upperValue)
	if !ok {
		t.Fatal("Result should carry the concrete custom value type")
	}
	if uv.v != "ABC" {
		t.Errorf("Custom value should be set by the session, got %q", uv.v)
	}
	// The live flag exposes the session value through the normal accessors.
	live, ok := c.Flags().Lookup("custom").Value.(*upperValue)
	if !ok || live.v != "ABC" {
		t.Errorf("Live flag should expose session value, got %v", c.Flags().Lookup("custom").Value)
	}
	s1.End()

	if got := c.Flags().Lookup("custom").Value.String(); got != "def" {
		t.Errorf("Custom value should be restored, got %q", got)
	}
	if c.Flags().Changed("custom") {
		t.Error("Changed should be restored to false")
	}

	s2 := c.NewParseSession()
	res2, err := s2.Parse(nil)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got := res2.Lookup("custom").Value.String(); got != "def" {
		t.Errorf("Second session should start from default, got %q", got)
	}
	s2.End()
}

func TestParseSessionParseFailure(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().String("tag", "default", "tag usage")

	s := c.NewParseSession()
	_, err := s.Parse([]string{"--tag=a", "--unknown"})
	if err == nil {
		t.Fatal("Expected a parse error for an unknown flag")
	}

	// A failed parse restores everything, including the flag parsed before
	// the failure.
	if got := mustGetString(t, c, "tag"); got != "default" {
		t.Errorf("After failed parse, flag should be restored, got %q", got)
	}
	if c.Flags().Changed("tag") {
		t.Error("After failed parse, Changed should be restored to false")
	}

	// End after a failed Parse is a no-op and safe.
	s.End()

	// The command remains usable for later sessions.
	s2 := c.NewParseSession()
	res, err := s2.Parse(nil)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := res.Flags().GetString("tag"); got != "default" {
		t.Errorf("Session after failure should see default, got %q", got)
	}
	s2.End()
}

func TestParseSessionPanic(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().String("tag", "default", "tag usage")

	s := c.NewParseSession()
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("Expected panic to propagate, got %v", r)
			}
		}()
		_ = s.Run([]string{"--tag=a"}, func(*ParseResult) error {
			panic("boom")
		})
	}()

	if got := mustGetString(t, c, "tag"); got != "default" {
		t.Errorf("After panic, flag should be restored, got %q", got)
	}
	if c.Flags().Changed("tag") {
		t.Error("After panic, Changed should be restored to false")
	}
}

func TestParseSessionRun(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().String("tag", "default", "tag usage")

	handlerErr := errors.New("handler failed")
	s := c.NewParseSession()
	var seen string
	err := s.Run([]string{"--tag=a"}, func(res *ParseResult) error {
		seen, _ = res.Flags().GetString("tag")
		// Normal flag accessors report the session value while running.
		if got := mustGetString(t, c, "tag"); got != "a" {
			t.Errorf("Handler should see session value, got %q", got)
		}
		return handlerErr
	})
	if !errors.Is(err, handlerErr) {
		t.Errorf("Run should return the handler error, got %v", err)
	}
	if seen != "a" {
		t.Errorf("Handler should receive the parsed result, got %q", seen)
	}
	if got := mustGetString(t, c, "tag"); got != "default" {
		t.Errorf("After Run, flag should be restored, got %q", got)
	}

	// Run with unparsable args returns the parse error and does not call fn.
	called := false
	if err := s.Run([]string{"--nope"}, func(*ParseResult) error {
		called = true
		return nil
	}); err == nil {
		t.Error("Expected a parse error from Run")
	}
	if called {
		t.Error("Handler must not be called when parsing fails")
	}
}

func TestParseSessionNested(t *testing.T) {
	c := &Command{Use: "root"}
	c.Flags().String("tag", "default", "tag usage")

	outer := c.NewParseSession()
	outerRes, err := outer.Parse([]string{"--tag=outer"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := outerRes.Flags().GetString("tag"); got != "outer" {
		t.Fatalf("Outer session value = %q", got)
	}

	// A nested session starts from the registered defaults, not from the
	// values of the enclosing session.
	inner := c.NewParseSession()
	innerRes, err := inner.Parse(nil)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := innerRes.Flags().GetString("tag"); got != "default" {
		t.Errorf("Nested session should start from default, got %q", got)
	}
	inner.End()

	// Ending the nested session restores the state of the enclosing one.
	if got := mustGetString(t, c, "tag"); got != "outer" {
		t.Errorf("After nested End, outer session value should be back, got %q", got)
	}

	// A nested session can also parse its own values.
	inner2 := c.NewParseSession()
	inner2Res, err := inner2.Parse([]string{"--tag=inner"})
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := inner2Res.Flags().GetString("tag"); got != "inner" {
		t.Errorf("Nested session value = %q", got)
	}
	if got := mustGetString(t, c, "tag"); got != "inner" {
		t.Errorf("Live flag should expose nested session value, got %q", got)
	}
	inner2.End()
	if got := mustGetString(t, c, "tag"); got != "outer" {
		t.Errorf("After nested End, outer session value should be back, got %q", got)
	}

	outer.End()
	if got := mustGetString(t, c, "tag"); got != "default" {
		t.Errorf("After outer End, flag should be restored, got %q", got)
	}
	if c.Flags().Changed("tag") {
		t.Error("After outer End, Changed should be restored to false")
	}
}

func TestParseSessionNestedAcrossCommands(t *testing.T) {
	parent := &Command{Use: "parent"}
	child := &Command{Use: "child"}
	parent.AddCommand(child)
	parent.PersistentFlags().String("tag", "default", "tag usage")

	outer := parent.NewParseSession()
	if _, err := outer.Parse([]string{"--tag=outer"}); err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}

	// A child session nested in a parent session shares the baseline of the
	// inherited flag: it starts from the registered default.
	inner := child.NewParseSession()
	res, err := inner.Parse(nil)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	if got, _ := res.Flags().GetString("tag"); got != "default" {
		t.Errorf("Nested child session should start from default, got %q", got)
	}
	inner.End()

	if got := mustGetString(t, parent, "tag"); got != "outer" {
		t.Errorf("After nested End, parent session value should be back, got %q", got)
	}
	outer.End()
	if got := mustGetString(t, parent, "tag"); got != "default" {
		t.Errorf("After outer End, flag should be restored, got %q", got)
	}
}
