package cobra

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// lifecycleChain builds a root -> group -> leaf command chain where every
// command acquires and releases a named resource, recording each step.
func lifecycleChain(log *[]string) (root, group, leaf *Command) {
	hooks := func(name string) (func(*Command, []string) error, func(*Command, []string) error) {
		return func(*Command, []string) error {
				*log = append(*log, "acquire "+name)
				return nil
			}, func(*Command, []string) error {
				*log = append(*log, "release "+name)
				return nil
			}
	}
	root = &Command{Use: "root"}
	group = &Command{Use: "group"}
	leaf = &Command{Use: "leaf", RunE: func(*Command, []string) error {
		*log = append(*log, "run")
		return nil
	}}
	root.AcquireResourcesE, root.ReleaseResourcesE = hooks("R")
	group.AcquireResourcesE, group.ReleaseResourcesE = hooks("G")
	leaf.AcquireResourcesE, leaf.ReleaseResourcesE = hooks("L")
	root.AddCommand(group)
	group.AddCommand(leaf)
	return root, group, leaf
}

func TestResourceLifecycleOrder(t *testing.T) {
	var log []string
	root, _, leaf := lifecycleChain(&log)
	leaf.PersistentPreRunE = func(*Command, []string) error {
		log = append(log, "persistent-pre-run")
		return nil
	}
	leaf.PreRunE = func(*Command, []string) error {
		log = append(log, "pre-run")
		return nil
	}
	leaf.PostRunE = func(*Command, []string) error {
		log = append(log, "post-run")
		return nil
	}
	leaf.PersistentPostRunE = func(*Command, []string) error {
		log = append(log, "persistent-post-run")
		return nil
	}

	if _, err := executeCommand(root, "group", "leaf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"persistent-pre-run",
		"pre-run",
		"acquire R", "acquire G", "acquire L",
		"run",
		"post-run",
		"persistent-post-run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("wrong lifecycle order:\n got: %v\nwant: %v", log, want)
	}
}

func TestResourceLifecycleAcquireFailureReleasesPartial(t *testing.T) {
	var log []string
	root, group, _ := lifecycleChain(&log)
	acquireErr := errors.New("cannot acquire G")
	group.AcquireResourcesE = func(*Command, []string) error {
		log = append(log, "acquire G")
		return acquireErr
	}

	_, err := executeCommand(root, "group", "leaf")
	if !errors.Is(err, acquireErr) {
		t.Fatalf("expected acquire error, got: %v", err)
	}

	// The leaf is never acquired, and only the already acquired root
	// resource is released.
	want := []string{"acquire R", "acquire G", "release R"}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("wrong cleanup chain:\n got: %v\nwant: %v", log, want)
	}
}

func TestResourceLifecycleRunEError(t *testing.T) {
	var log []string
	root, _, leaf := lifecycleChain(&log)
	runErr := errors.New("run failed")
	leaf.RunE = func(*Command, []string) error {
		log = append(log, "run")
		return runErr
	}

	_, err := executeCommand(root, "group", "leaf")
	if !errors.Is(err, runErr) {
		t.Fatalf("expected run error, got: %v", err)
	}
	if err != runErr {
		t.Errorf("expected the unwrapped run error, got: %v", err)
	}

	want := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("wrong cleanup chain:\n got: %v\nwant: %v", log, want)
	}
}

func TestResourceLifecycleReleaseErrorsAggregated(t *testing.T) {
	var log []string
	root, _, leaf := lifecycleChain(&log)
	runErr := errors.New("run failed")
	releaseErrL := errors.New("cannot release L")
	releaseErrR := errors.New("cannot release R")
	leaf.RunE = func(*Command, []string) error { return runErr }
	leaf.ReleaseResourcesE = func(*Command, []string) error {
		log = append(log, "release L")
		return releaseErrL
	}
	root.ReleaseResourcesE = func(*Command, []string) error {
		log = append(log, "release R")
		return releaseErrR
	}

	_, err := executeCommand(root, "group", "leaf")
	if err == nil {
		t.Fatal("expected an aggregated error, got nil")
	}
	for _, want := range []error{runErr, releaseErrL, releaseErrR} {
		if !errors.Is(err, want) {
			t.Errorf("aggregated error %q does not contain %q", err, want)
		}
	}
	for _, want := range []string{"run failed", "cannot release L", "cannot release R"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregated error %q does not mention %q", err, want)
		}
	}

	// Releases still happen in reverse order even when some of them fail.
	wantLog := []string{
		"acquire R", "acquire G", "acquire L",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("wrong cleanup chain:\n got: %v\nwant: %v", log, wantLog)
	}
}

func TestResourceLifecycleContextCancellation(t *testing.T) {
	var log []string
	root, _, leaf := lifecycleChain(&log)
	ctx, cancel := context.WithCancel(context.Background())
	leaf.RunE = func(cmd *Command, _ []string) error {
		cancel()
		return cmd.Context().Err()
	}

	_, err := executeCommandWithContext(ctx, root, "group", "leaf")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	want := []string{
		"acquire R", "acquire G", "acquire L",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("wrong cleanup chain:\n got: %v\nwant: %v", log, want)
	}
}

func TestResourceLifecycleConsecutiveExecutes(t *testing.T) {
	var log []string
	root, _, _ := lifecycleChain(&log)

	for i := 0; i < 2; i++ {
		if _, err := executeCommand(root, "group", "leaf"); err != nil {
			t.Fatalf("execution %d: unexpected error: %v", i+1, err)
		}
	}

	cycle := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	want := append(append([]string{}, cycle...), cycle...)
	if !reflect.DeepEqual(log, want) {
		t.Errorf("each Execute must run a full independent lifecycle:\n got: %v\nwant: %v", log, want)
	}
}

func TestResourceLifecycleNestedExecute(t *testing.T) {
	var log []string
	outer, _, leaf := lifecycleChain(&log)

	// An independent command tree executed from within the outer RunE.
	var innerLog []string
	inner, _, _ := lifecycleChain(&innerLog)
	inner.SetArgs([]string{"group", "leaf"})
	leaf.RunE = func(*Command, []string) error {
		log = append(log, "run")
		if err := inner.Execute(); err != nil {
			return fmt.Errorf("inner execute: %w", err)
		}
		log = append(log, "inner done")
		return nil
	}

	if _, err := executeCommand(outer, "group", "leaf"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The nested execution completes its whole lifecycle (including releases)
	// before the outer cleanup stack is unwound; the two stacks never mix.
	wantInner := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(innerLog, wantInner) {
		t.Errorf("wrong inner lifecycle:\n got: %v\nwant: %v", innerLog, wantInner)
	}
	wantOuter := []string{
		"acquire R", "acquire G", "acquire L",
		"run",
		"inner done",
		"release L", "release G", "release R",
	}
	if !reflect.DeepEqual(log, wantOuter) {
		t.Errorf("wrong outer lifecycle:\n got: %v\nwant: %v", log, wantOuter)
	}
}

func TestResourceLifecycleNotAcquired(t *testing.T) {
	acquireErr := errors.New("invalid arguments")
	cases := []struct {
		name    string
		args    []string
		prepare func(root *Command)
	}{
		{"help flag", []string{"--help"}, nil},
		{"version flag", []string{"--version"}, func(root *Command) { root.Version = "1.0.0" }},
		{"flag parse error", []string{"--unknown-flag"}, nil},
		{"args validation error", []string{"unexpected"},
			func(root *Command) {
				root.Args = func(*Command, []string) error { return acquireErr }
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var acquires, releases int
			root := &Command{
				Use: "root",
				RunE: func(*Command, []string) error {
					t.Error("RunE must not be called")
					return nil
				},
				AcquireResourcesE: func(*Command, []string) error {
					acquires++
					return nil
				},
				ReleaseResourcesE: func(*Command, []string) error {
					releases++
					return nil
				},
			}
			if tc.prepare != nil {
				tc.prepare(root)
			}
			_, _ = executeCommand(root, tc.args...)
			if acquires != 0 || releases != 0 {
				t.Errorf("no resources may be acquired on %s: acquires=%d releases=%d",
					tc.name, acquires, releases)
			}
		})
	}
}

// ExampleCommand demonstrates the resource lifecycle hooks: first a
// successful root/group/leaf execution, then a failing acquisition where only
// the already acquired resources are released. Run it with:
//
//	go test -run ExampleCommand -v
func ExampleCommand() {
	newChain := func(groupAcquireErr error) *Command {
		hooks := func(name string) (func(*Command, []string) error, func(*Command, []string) error) {
			return func(*Command, []string) error {
					fmt.Println("acquire " + name)
					if name == "G" {
						return groupAcquireErr
					}
					return nil
				}, func(*Command, []string) error {
					fmt.Println("release " + name)
					return nil
				}
		}
		root := &Command{Use: "root", SilenceUsage: true, SilenceErrors: true}
		group := &Command{Use: "group"}
		leaf := &Command{Use: "leaf", RunE: func(*Command, []string) error {
			fmt.Println("run")
			return nil
		}}
		root.AcquireResourcesE, root.ReleaseResourcesE = hooks("R")
		group.AcquireResourcesE, group.ReleaseResourcesE = hooks("G")
		leaf.AcquireResourcesE, leaf.ReleaseResourcesE = hooks("L")
		root.AddCommand(group)
		group.AddCommand(leaf)
		return root
	}

	fmt.Println("== success chain ==")
	success := newChain(nil)
	success.SetArgs([]string{"group", "leaf"})
	if err := success.Execute(); err != nil {
		fmt.Println("error:", err)
	}

	fmt.Println("== failure cleanup chain ==")
	failure := newChain(errors.New("cannot acquire G"))
	failure.SetArgs([]string{"group", "leaf"})
	if err := failure.Execute(); err != nil {
		fmt.Println("error:", err)
	}

	// Output:
	// == success chain ==
	// acquire R
	// acquire G
	// acquire L
	// run
	// release L
	// release G
	// release R
	// == failure cleanup chain ==
	// acquire R
	// acquire G
	// release R
	// error: cannot acquire G
}
