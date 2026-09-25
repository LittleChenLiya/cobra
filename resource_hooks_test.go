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
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// resourceChain builds a root/group/leaf command tree in which every command
// acquires and releases a resource named by resources, logging each step into
// log. acquireErr and releaseErr optionally fail the hooks of the named
// resources.
func resourceChain(log *[]string, resources map[string]string, acquireErr, releaseErr map[string]error) (root, group, leaf *Command) {
	hooks := func(name string) (func(cmd *Command, args []string) error, func(cmd *Command, args []string) error) {
		resource := resources[name]
		return func(cmd *Command, args []string) error {
				*log = append(*log, "acquire "+resource)
				return acquireErr[resource]
			}, func(cmd *Command, args []string) error {
				*log = append(*log, "release "+resource)
				return releaseErr[resource]
			}
	}

	leaf = &Command{Use: "leaf"}
	group = &Command{Use: "group"}
	root = &Command{Use: "root"}

	root.AcquireResourceE, root.ReleaseResourceE = hooks("root")
	group.AcquireResourceE, group.ReleaseResourceE = hooks("group")
	leaf.AcquireResourceE, leaf.ReleaseResourceE = hooks("leaf")

	leaf.RunE = func(cmd *Command, args []string) error {
		*log = append(*log, "run")
		return nil
	}

	root.AddCommand(group)
	group.AddCommand(leaf)
	return root, group, leaf
}

func rgcResources() map[string]string {
	return map[string]string{"root": "R", "group": "G", "leaf": "L"}
}

func TestResourceLifecycleSuccessOrder(t *testing.T) {
	var log []string
	root, _, _ := resourceChain(&log, rgcResources(), nil, nil)

	if _, err := executeCommand(root, "group", "leaf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}

	// Double-check that the release order is exactly the reverse of the
	// acquisition order.
	var acquired, released []string
	for _, entry := range log {
		if strings.HasPrefix(entry, "acquire ") {
			acquired = append(acquired, strings.TrimPrefix(entry, "acquire "))
		}
		if strings.HasPrefix(entry, "release ") {
			released = append(released, strings.TrimPrefix(entry, "release "))
		}
	}
	for i := range acquired {
		if acquired[i] != released[len(released)-1-i] {
			t.Errorf("release order %v is not the reverse of acquire order %v", released, acquired)
			break
		}
	}
}

func TestResourceLifecycleAcquireFailureReleasesOnlyAcquired(t *testing.T) {
	acquireErr := map[string]error{"G": errors.New("group acquire failed")}
	var log []string
	root, _, _ := resourceChain(&log, rgcResources(), acquireErr, nil)

	_, err := executeCommand(root, "group", "leaf")
	if err == nil || !strings.Contains(err.Error(), "group acquire failed") {
		t.Fatalf("expected group acquire error, got %v", err)
	}

	expected := []string{"acquire R", "acquire G", "release R"}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

func TestResourceLifecycleRunErrorStillReleases(t *testing.T) {
	runErr := errors.New("run failed")
	var log []string
	root, _, leaf := resourceChain(&log, rgcResources(), nil, nil)
	leaf.RunE = func(cmd *Command, args []string) error {
		log = append(log, "run")
		return runErr
	}

	_, err := executeCommand(root, "group", "leaf")
	if !errors.Is(err, runErr) {
		t.Fatalf("expected run error, got %v", err)
	}

	expected := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

func TestResourceLifecycleAggregatesRunAndReleaseErrors(t *testing.T) {
	runErr := errors.New("run failed")
	releaseLeafErr := errors.New("leaf release failed")
	releaseRootErr := errors.New("root release failed")
	releaseErr := map[string]error{"L": releaseLeafErr, "R": releaseRootErr}

	var log []string
	root, _, leaf := resourceChain(&log, rgcResources(), nil, releaseErr)
	leaf.RunE = func(cmd *Command, args []string) error {
		log = append(log, "run")
		return runErr
	}

	_, err := executeCommand(root, "group", "leaf")
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	for _, want := range []error{runErr, releaseLeafErr, releaseRootErr} {
		if !errors.Is(err, want) {
			t.Errorf("aggregated error %q does not contain %q", err, want)
		}
	}

	// All releases must run, in reverse order, despite multiple failures.
	expected := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

func TestResourceLifecycleAcquireFailureAggregatesReleaseError(t *testing.T) {
	acquireErr := map[string]error{"G": errors.New("group acquire failed")}
	releaseErr := map[string]error{"R": errors.New("root release failed")}
	var log []string
	root, _, _ := resourceChain(&log, rgcResources(), acquireErr, releaseErr)

	_, err := executeCommand(root, "group", "leaf")
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	if !errors.Is(err, acquireErr["G"]) || !errors.Is(err, releaseErr["R"]) {
		t.Errorf("aggregated error %q missing acquire or release failure", err)
	}

	expected := []string{"acquire R", "acquire G", "release R"}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

func TestResourceLifecycleContextCancel(t *testing.T) {
	releaseErr := errors.New("release failed")
	var log []string
	root, _, leaf := resourceChain(&log, rgcResources(), nil, map[string]error{"G": releaseErr})

	ctx, cancel := context.WithCancel(context.Background())
	leaf.RunE = func(cmd *Command, args []string) error {
		cancel()
		log = append(log, "run")
		return ctx.Err()
	}

	_, err := executeCommandWithContext(ctx, root, "group", "leaf")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in aggregated error, got %v", err)
	}
	if !errors.Is(err, releaseErr) {
		t.Errorf("expected release error in aggregated error, got %v", err)
	}

	expected := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

func TestResourceLifecycleConsecutiveExecutes(t *testing.T) {
	var log []string
	root, _, _ := resourceChain(&log, rgcResources(), nil, nil)

	expected := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	for i := 0; i < 2; i++ {
		log = nil
		if _, err := executeCommand(root, "group", "leaf"); err != nil {
			t.Fatalf("execution %d: unexpected error: %v", i, err)
		}
		if !reflect.DeepEqual(log, expected) {
			t.Errorf("execution %d: expected lifecycle %v, got %v", i, expected, log)
		}
	}
}

func TestResourceLifecycleNestedExecutesDoNotShareStack(t *testing.T) {
	var log []string

	innerLeaf := &Command{Use: "inner"}
	innerRoot := &Command{Use: "innerroot"}
	innerRoot.AcquireResourceE = func(cmd *Command, args []string) error {
		log = append(log, "acquire inner")
		return nil
	}
	innerRoot.ReleaseResourceE = func(cmd *Command, args []string) error {
		log = append(log, "release inner")
		return nil
	}
	innerLeaf.RunE = func(cmd *Command, args []string) error {
		log = append(log, "run inner")
		return nil
	}
	innerRoot.AddCommand(innerLeaf)

	outerLeaf := &Command{Use: "outer"}
	outerRoot := &Command{Use: "outerroot"}
	outerRoot.AcquireResourceE = func(cmd *Command, args []string) error {
		log = append(log, "acquire outer")
		return nil
	}
	outerRoot.ReleaseResourceE = func(cmd *Command, args []string) error {
		log = append(log, "release outer")
		return nil
	}
	outerLeaf.RunE = func(cmd *Command, args []string) error {
		log = append(log, "run outer")
		innerRoot.SetArgs([]string{"inner"})
		return innerRoot.Execute()
	}
	outerRoot.AddCommand(outerLeaf)

	if _, err := executeCommand(outerRoot, "outer"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{
		"acquire outer",
		"run outer",
		"acquire inner",
		"run inner",
		"release inner",
		"release outer",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

func TestResourceLifecycleNotAcquiredOnHelpVersionAndParseFailures(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"help flag", []string{"group", "leaf", "--help"}},
		{"help command", []string{"help", "group", "leaf"}},
		{"version flag", []string{"group", "leaf", "--version"}},
		{"unknown flag", []string{"group", "leaf", "--no-such-flag"}},
		{"invalid args", []string{"group", "leaf", "unexpected"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acquires := 0
			releases := 0
			counting := func(cmd *Command, args []string) error {
				acquires++
				return nil
			}
			releasing := func(cmd *Command, args []string) error {
				releases++
				return nil
			}
			leaf := &Command{
				Use:  "leaf",
				Args: NoArgs,
				RunE: func(cmd *Command, args []string) error { return nil },
			}
			group := &Command{Use: "group"}
			root := &Command{Use: "root", Version: "1.0.0"}
			for _, c := range []*Command{root, group, leaf} {
				c.AcquireResourceE = counting
				c.ReleaseResourceE = releasing
			}
			root.AddCommand(group)
			group.AddCommand(leaf)

			// The command may or may not return an error; what matters is
			// that no resource was ever acquired.
			_, _ = executeCommand(root, tc.args...)
			if acquires != 0 {
				t.Errorf("expected no AcquireResourceE calls, got %d", acquires)
			}
			if releases != 0 {
				t.Errorf("expected no ReleaseResourceE calls, got %d", releases)
			}
		})
	}
}

func TestResourceLifecycleHookOrderWithExistingHooks(t *testing.T) {
	var log []string
	root, group, leaf := resourceChain(&log, rgcResources(), nil, nil)

	root.PersistentPreRunE = func(cmd *Command, args []string) error {
		log = append(log, "persistent-pre-run")
		return nil
	}
	leaf.PreRunE = func(cmd *Command, args []string) error {
		log = append(log, "pre-run")
		return nil
	}
	leaf.PostRunE = func(cmd *Command, args []string) error {
		log = append(log, "post-run")
		return nil
	}
	group.PersistentPostRunE = func(cmd *Command, args []string) error {
		log = append(log, "persistent-post-run")
		return nil
	}

	if _, err := executeCommand(root, "group", "leaf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{
		"persistent-pre-run",
		"pre-run",
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
		"post-run",
		"persistent-post-run",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Errorf("expected lifecycle %v, got %v", expected, log)
	}
}

// ExampleCommand_resourceLifecycle demonstrates the resource lifecycle hooks:
// first a successful execution acquiring R/G/L and releasing L/G/R, then a
// failing acquisition which only releases the resources already acquired.
func ExampleCommand_resourceLifecycle() {
	newRoot := func(groupAcquireErr error) *Command {
		hook := func(resource string, acquireErr error) (func(cmd *Command, args []string) error, func(cmd *Command, args []string) error) {
			return func(cmd *Command, args []string) error {
					fmt.Println("acquire " + resource)
					return acquireErr
				}, func(cmd *Command, args []string) error {
					fmt.Println("release " + resource)
					return nil
				}
		}
		root := &Command{Use: "root", SilenceErrors: true, SilenceUsage: true}
		group := &Command{Use: "group"}
		leaf := &Command{Use: "leaf", RunE: func(cmd *Command, args []string) error {
			fmt.Println("run")
			return nil
		}}
		root.AcquireResourceE, root.ReleaseResourceE = hook("R", nil)
		group.AcquireResourceE, group.ReleaseResourceE = hook("G", groupAcquireErr)
		leaf.AcquireResourceE, leaf.ReleaseResourceE = hook("L", nil)
		root.AddCommand(group)
		group.AddCommand(leaf)
		return root
	}

	fmt.Println("success chain:")
	success := newRoot(nil)
	success.SetArgs([]string{"group", "leaf"})
	if err := success.Execute(); err != nil {
		fmt.Println("error:", err)
	}

	fmt.Println("failure cleanup chain:")
	failure := newRoot(errors.New("cannot acquire G"))
	failure.SetArgs([]string{"group", "leaf"})
	if err := failure.Execute(); err != nil {
		fmt.Println("error:", err)
	}

	// Output:
	// success chain:
	// acquire R
	// acquire G
	// acquire L
	// run
	// release L
	// release G
	// release R
	// failure cleanup chain:
	// acquire R
	// acquire G
	// release R
	// error: cannot acquire G
}

