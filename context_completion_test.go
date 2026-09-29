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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCompletionClock is a manually advanced CompletionClock.
type fakeCompletionClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeCompletionClock() *fakeCompletionClock {
	return &fakeCompletionClock{now: time.Unix(1000, 0)}
}

func (f *fakeCompletionClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeCompletionClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// newContextCompletionCmd returns a command with a single string flag and
// the registered completion function for that flag.
func newContextCompletionCmd(t *testing.T, fn CompletionFuncWithContext, opts ...CompletionOption) (*Command, CompletionFunc) {
	t.Helper()
	cmd := &Command{Use: "root"}
	cmd.Flags().String("name", "", "a name flag")
	if err := cmd.RegisterFlagCompletionFuncWithContext("name", fn, opts...); err != nil {
		t.Fatalf("RegisterFlagCompletionFuncWithContext: %v", err)
	}
	completionFn, ok := cmd.GetFlagCompletionFunc("name")
	if !ok {
		t.Fatal("expected a completion function to be registered")
	}
	return cmd, completionFn
}

func setFlag(t *testing.T, cmd *Command, value string) {
	t.Helper()
	if err := cmd.Flags().Set("name", value); err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}
}

func TestContextCompletionCacheHitFlagChangeAndExpiry(t *testing.T) {
	clock := newFakeCompletionClock()
	var calls int
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		calls++
		return []Completion{"res"}, ShellCompDirectiveNoFileComp, nil
	}
	cmd, completionFn := newContextCompletionCmd(t, fn,
		WithCompletionCache(10, time.Minute, "v1"),
		WithCompletionClock(clock.Now),
	)

	setFlag(t, cmd, "a")
	// First call executes the callback.
	comps, directive := completionFn(cmd, nil, "")
	if calls != 1 {
		t.Fatalf("expected 1 callback execution, got %d", calls)
	}
	if len(comps) != 1 || comps[0] != "res" || directive != ShellCompDirectiveNoFileComp {
		t.Fatalf("unexpected completion result: %v, %v", comps, directive)
	}
	// Same parameters are served from the cache.
	if _, _ = completionFn(cmd, nil, ""); calls != 1 {
		t.Fatalf("expected cache hit with identical parameters, got %d calls", calls)
	}
	// A different flag value misses the cache and re-executes the callback.
	setFlag(t, cmd, "b")
	if _, _ = completionFn(cmd, nil, ""); calls != 2 {
		t.Fatalf("expected re-execution after flag change, got %d calls", calls)
	}
	// Advancing the clock past the TTL expires the entry.
	clock.Advance(2 * time.Minute)
	if _, _ = completionFn(cmd, nil, ""); calls != 3 {
		t.Fatalf("expected re-execution after cache expiry, got %d calls", calls)
	}
	// The fresh entry is cached again.
	if _, _ = completionFn(cmd, nil, ""); calls != 3 {
		t.Fatalf("expected cache hit after re-population, got %d calls", calls)
	}
}

func TestContextCompletionCacheVersionAndWordInKey(t *testing.T) {
	var calls int
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		calls++
		return []Completion{"res"}, ShellCompDirectiveNoFileComp, nil
	}
	cmd, completionFn := newContextCompletionCmd(t, fn, WithCompletionCache(10, 0, "v1"))

	completionFn(cmd, nil, "")
	completionFn(cmd, nil, "")
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	// A different word to complete is a different cache key.
	completionFn(cmd, nil, "x")
	if calls != 2 {
		t.Fatalf("expected re-execution for a different word, got %d calls", calls)
	}
}

func TestContextCompletionCacheKeyIncludesCommandPath(t *testing.T) {
	var calls int
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		calls++
		return []Completion{"res"}, ShellCompDirectiveNoFileComp, nil
	}
	root, completionFn := newContextCompletionCmd(t, fn, WithCompletionCache(10, 0, "v1"))
	child := &Command{Use: "child"}
	child.Flags().String("name", "", "same flag name on a different command")
	root.AddCommand(child)

	setFlag(t, root, "a")
	setFlag(t, child, "a")

	// Same flag name and value, but different command paths: no cache hit.
	completionFn(root, nil, "")
	completionFn(child, nil, "")
	if calls != 2 {
		t.Fatalf("expected distinct cache keys per command path, got %d calls", calls)
	}
	// Both entries are now cached.
	completionFn(root, nil, "")
	completionFn(child, nil, "")
	if calls != 2 {
		t.Fatalf("expected cache hits for both commands, got %d calls", calls)
	}
}

func TestContextCompletionCacheLRUEvictionOrder(t *testing.T) {
	clock := newFakeCompletionClock()
	var calls int
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		calls++
		return []Completion{"res"}, ShellCompDirectiveNoFileComp, nil
	}
	cmd, completionFn := newContextCompletionCmd(t, fn,
		WithCompletionCache(2, 0, "v1"),
		WithCompletionClock(clock.Now),
	)

	complete := func(value string) {
		t.Helper()
		setFlag(t, cmd, value)
		completionFn(cmd, nil, "")
	}

	complete("a") // miss, cache: a
	complete("b") // miss, cache: b, a
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	complete("a") // hit, a becomes most recently used: a, b
	if calls != 2 {
		t.Fatalf("expected cache hit for a, got %d calls", calls)
	}
	complete("c") // miss, evicts b (least recently used): c, a
	if calls != 3 {
		t.Fatalf("expected miss for c, got %d calls", calls)
	}
	complete("b") // miss, b was evicted: evicts a: b, c
	if calls != 4 {
		t.Fatalf("expected b to have been evicted, got %d calls", calls)
	}
	complete("a") // miss, a was evicted: evicts c: a, b
	if calls != 5 {
		t.Fatalf("expected a to have been evicted, got %d calls", calls)
	}
	complete("b") // hit
	if calls != 5 {
		t.Fatalf("expected cache hit for b, got %d calls", calls)
	}
}

func TestContextCompletionConcurrentSameKey(t *testing.T) {
	var calls int32
	started := make(chan struct{})
	gate := make(chan struct{})
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(started)
		}
		<-gate
		return []Completion{"res"}, ShellCompDirectiveNoFileComp, nil
	}
	cmd, completionFn := newContextCompletionCmd(t, fn, WithCompletionCache(10, 0, "v1"))

	const workers = 8
	var wg sync.WaitGroup
	results := make([][]Completion, workers)
	directives := make([]ShellCompDirective, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], directives[i] = completionFn(cmd, nil, "")
		}(i)
	}

	<-started
	// Give the other goroutines a chance to block on the in-flight call.
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected the callback to run once for concurrent identical requests, got %d", got)
	}
	for i := 0; i < workers; i++ {
		if len(results[i]) != 1 || results[i][0] != "res" || directives[i] != ShellCompDirectiveNoFileComp {
			t.Fatalf("worker %d got unexpected result: %v, %v", i, results[i], directives[i])
		}
	}
	// The result was cached by the leader.
	completionFn(cmd, nil, "")
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected cache hit after concurrent requests, got %d calls", got)
	}
}

func TestContextCompletionErrorNotCached(t *testing.T) {
	var calls int
	fail := true
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		calls++
		if fail {
			return nil, ShellCompDirectiveError, errors.New("boom")
		}
		return []Completion{"res"}, ShellCompDirectiveNoFileComp, nil
	}
	cmd, completionFn := newContextCompletionCmd(t, fn, WithCompletionCache(10, 0, "v1"))

	_, directive := completionFn(cmd, nil, "")
	if directive != ShellCompDirectiveError {
		t.Fatalf("expected ShellCompDirectiveError on callback error, got %v", directive)
	}
	// Errors are not cached: the callback runs again.
	completionFn(cmd, nil, "")
	if calls != 2 {
		t.Fatalf("expected error results not to be cached, got %d calls", calls)
	}
	// A subsequent success is cached.
	fail = false
	completionFn(cmd, nil, "")
	completionFn(cmd, nil, "")
	if calls != 3 {
		t.Fatalf("expected successful result to be cached, got %d calls", calls)
	}
}

func TestContextCompletionTimeoutDoesNotBlock(t *testing.T) {
	var calls int32
	unblock := make(chan struct{})
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		atomic.AddInt32(&calls, 1)
		select {
		case <-unblock:
			return []Completion{"late"}, ShellCompDirectiveNoFileComp, nil
		case <-ctx.Done():
			return nil, ShellCompDirectiveError, ctx.Err()
		}
	}
	cmd, completionFn := newContextCompletionCmd(t, fn,
		WithCompletionTimeout(50*time.Millisecond),
		WithCompletionCache(10, 0, "v1"),
	)

	start := time.Now()
	_, directive := completionFn(cmd, nil, "")
	if directive != ShellCompDirectiveError {
		t.Fatalf("expected ShellCompDirectiveError on timeout, got %v", directive)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("completion blocked past the timeout: %v", elapsed)
	}
	// The next completion request is not blocked by the abandoned callback.
	start = time.Now()
	completionFn(cmd, nil, "")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("next completion blocked by the abandoned callback: %v", elapsed)
	}
	close(unblock)
}

func TestContextCompletionCancelDiscardsLateResult(t *testing.T) {
	var calls int32
	started := make(chan struct{})
	unblock := make(chan struct{})
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		atomic.AddInt32(&calls, 1)
		select {
		case <-started:
		default:
			close(started)
		}
		<-unblock
		return []Completion{"stale"}, ShellCompDirectiveNoFileComp, nil
	}
	cmd, completionFn := newContextCompletionCmd(t, fn, WithCompletionCache(10, 0, "v1"))

	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	done := make(chan ShellCompDirective, 1)
	go func() {
		_, directive := completionFn(cmd, nil, "")
		done <- directive
	}()
	<-started
	cancel()
	if directive := <-done; directive != ShellCompDirectiveError {
		t.Fatalf("expected ShellCompDirectiveError after cancellation, got %v", directive)
	}
	// The old callback returns late; its result must not enter the cache.
	close(unblock)
	time.Sleep(100 * time.Millisecond)

	// A new request with a fresh context re-executes the callback instead of
	// observing the stale result, and is not blocked by the abandoned call.
	cmd.SetContext(context.Background())
	fresh := make(chan []Completion, 1)
	go func() {
		comps, _ := completionFn(cmd, nil, "")
		fresh <- comps
	}()
	select {
	case comps := <-fresh:
		if len(comps) != 1 || comps[0] != "stale" {
			t.Fatalf("unexpected completions: %v", comps)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("next completion blocked after cancellation")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected the callback to be re-executed after cancellation, got %d calls", got)
	}
}

func TestRegisterFlagCompletionFuncWithContextErrors(t *testing.T) {
	cmd := &Command{Use: "root"}
	cmd.Flags().String("name", "", "a name flag")
	fn := func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error) {
		return nil, ShellCompDirectiveNoFileComp, nil
	}

	if err := cmd.RegisterFlagCompletionFuncWithContext("unknown", fn); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
	if err := cmd.RegisterFlagCompletionFuncWithContext("name", fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cmd.RegisterFlagCompletionFuncWithContext("name", fn); err == nil {
		t.Fatal("expected an error when registering the same flag twice")
	}
}
