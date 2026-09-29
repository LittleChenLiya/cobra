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
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/pflag"
)

// CompletionFuncWithContext is a context-aware function that provides
// completion results.
//
// The provided context carries the completion timeout configured with
// [WithCompletionTimeout] and any cancellation of the command's context.
// A completion function should abort its work when the context is done.
// Results are only cached when the function returns a nil error.
type CompletionFuncWithContext func(ctx context.Context, cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective, error)

// CompletionClock returns the current time. It is used to determine cache
// entry expiration and can be replaced in tests with
// [WithCompletionClock].
type CompletionClock func() time.Time

// completionConfig holds the configuration for a context-aware flag
// completion registration.
type completionConfig struct {
	timeout      time.Duration
	cacheSize    int
	cacheTTL     time.Duration
	cacheVersion string
	clock        CompletionClock
}

// CompletionOption configures a context-aware flag completion registered
// with [Command.RegisterFlagCompletionFuncWithContext].
type CompletionOption func(*completionConfig)

// WithCompletionTimeout bounds the time a single completion callback may
// run. When the timeout elapses, the completion request fails with
// [ShellCompDirectiveError]; the late result of the callback is discarded
// and never reaches the cache, and subsequent completion requests are not
// blocked by the abandoned callback.
func WithCompletionTimeout(timeout time.Duration) CompletionOption {
	return func(cfg *completionConfig) {
		cfg.timeout = timeout
	}
}

// WithCompletionCache enables an LRU cache of successful completion
// results with the given maximum number of entries and time-to-live.
//
// The cache key is built from the command path, the values of the flags
// that have been set on the command, the word being completed, and the
// caller-provided version. Changing the version invalidates previous
// entries because they can no longer be matched. A ttl of zero or less
// disables expiration. A capacity of zero or less disables the cache.
func WithCompletionCache(capacity int, ttl time.Duration, version string) CompletionOption {
	return func(cfg *completionConfig) {
		cfg.cacheSize = capacity
		cfg.cacheTTL = ttl
		cfg.cacheVersion = version
	}
}

// WithCompletionClock sets the clock used to expire cache entries. It is
// mostly useful for tests. The default is [time.Now].
func WithCompletionClock(clock CompletionClock) CompletionOption {
	return func(cfg *completionConfig) {
		if clock != nil {
			cfg.clock = clock
		}
	}
}

// RegisterFlagCompletionFuncWithContext registers a context-aware function
// to provide completion for a flag. It behaves like
// [Command.RegisterFlagCompletionFunc] but the completion function receives
// the command's context (extended with the timeout configured through
// [WithCompletionTimeout]) and reports errors explicitly.
//
// The completion callback is always run in its own goroutine so that a
// timeout or a cancellation cannot block subsequent completion requests;
// the late result of an abandoned callback is discarded and is never
// cached, so it cannot overwrite newer results.
func (c *Command) RegisterFlagCompletionFuncWithContext(flagName string, f CompletionFuncWithContext, opts ...CompletionOption) error {
	flag := c.Flag(flagName)
	if flag == nil {
		return fmt.Errorf("RegisterFlagCompletionFuncWithContext: flag '%s' does not exist", flagName)
	}

	cfg := &completionConfig{clock: time.Now}
	for _, opt := range opts {
		opt(cfg)
	}

	runner := &contextualCompletion{fn: f, timeout: cfg.timeout}
	if cfg.cacheSize > 0 {
		runner.cache = newCompletionCache(cfg.cacheSize, cfg.cacheTTL, cfg.cacheVersion, cfg.clock)
		runner.group = &completionGroup{calls: make(map[string]*completionCall)}
	}

	wrapped := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return runner.complete(cmd, args, toComplete)
	}

	flagCompletionMutex.Lock()
	defer flagCompletionMutex.Unlock()

	if _, exists := flagCompletionFunctions[flag]; exists {
		return fmt.Errorf("RegisterFlagCompletionFuncWithContext: flag '%s' already registered", flagName)
	}
	flagCompletionFunctions[flag] = wrapped
	return nil
}

// completionResult is the outcome of a single completion callback run.
type completionResult struct {
	completions []Completion
	directive   ShellCompDirective
	err         error
}

// contextualCompletion runs a CompletionFuncWithContext with a timeout and
// an optional deduplicating cache.
type contextualCompletion struct {
	fn      CompletionFuncWithContext
	timeout time.Duration
	cache   *completionCache
	group   *completionGroup
}

func (r *contextualCompletion) complete(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	if r.cache == nil {
		res := r.run(ctx, cmd, args, toComplete)
		if res.err != nil {
			return nil, ShellCompDirectiveError
		}
		return res.completions, res.directive
	}

	key := r.cache.key(cmd, toComplete)
	if completions, directive, ok := r.cache.get(key); ok {
		return completions, directive
	}

	res, leader := r.group.do(ctx, key, func(ctx context.Context) completionResult {
		return r.run(ctx, cmd, args, toComplete)
	})
	if res.err != nil {
		return nil, ShellCompDirectiveError
	}
	if leader {
		// Only successful results are cached, and only by the goroutine
		// that actually produced them. Followers either observe the cached
		// value on a later call or share the leader's result.
		r.cache.set(key, res.completions, res.directive)
	}
	return res.completions, res.directive
}

// run executes the completion callback in a goroutine so that a slow or
// blocked callback cannot block the caller past the context's deadline.
// The result channel is buffered, so a late callback never blocks and its
// result is simply discarded once the context is done.
func (r *contextualCompletion) run(ctx context.Context, cmd *Command, args []string, toComplete string) completionResult {
	resCh := make(chan completionResult, 1)
	go func() {
		completions, directive, err := r.fn(ctx, cmd, args, toComplete)
		resCh <- completionResult{completions: completions, directive: directive, err: err}
	}()

	select {
	case res := <-resCh:
		return res
	case <-ctx.Done():
		return completionResult{err: ctx.Err()}
	}
}

// completionCall represents an in-flight or completed execution of a
// completion callback for a given cache key.
type completionCall struct {
	done chan struct{}
	res  completionResult
}

// completionGroup deduplicates concurrent completion requests for the same
// cache key: the first request executes the callback while concurrent
// requests wait for its result.
type completionGroup struct {
	mu    sync.Mutex
	calls map[string]*completionCall
}

// do runs fn for the given key, sharing the outcome with concurrent callers
// for the same key. It reports whether the caller is the leader that
// produced the result (and is therefore responsible for caching it).
//
// If the context is done while waiting, the in-flight call is abandoned:
// it is removed from the group so the next request starts a fresh execution
// instead of blocking on the stale one, and the late result is discarded
// instead of being cached.
func (g *completionGroup) do(ctx context.Context, key string, fn func(context.Context) completionResult) (completionResult, bool) {
	g.mu.Lock()
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-call.done:
			return call.res, false
		case <-ctx.Done():
			return completionResult{err: ctx.Err()}, false
		}
	}
	call := &completionCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	resCh := make(chan completionResult, 1)
	go func() {
		resCh <- fn(ctx)
	}()

	select {
	case res := <-resCh:
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		call.res = res
		close(call.done)
		return res, true
	case <-ctx.Done():
		err := ctx.Err()
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		// Wake any followers that started waiting before the call was
		// abandoned; the late result is discarded by the buffered channel.
		call.res = completionResult{err: err}
		close(call.done)
		return completionResult{err: err}, false
	}
}

// completionCacheEntry is a single cached completion result.
type completionCacheEntry struct {
	key         string
	completions []Completion
	directive   ShellCompDirective
	expiresAt   time.Time // zero value means the entry never expires
}

// completionCache is a bounded LRU cache of successful completion results.
// Expiration is evaluated against an injectable clock.
type completionCache struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	version  string
	clock    CompletionClock
	lru      *list.List // front is the most recently used entry
	entries  map[string]*list.Element
}

func newCompletionCache(capacity int, ttl time.Duration, version string, clock CompletionClock) *completionCache {
	if clock == nil {
		clock = time.Now
	}
	return &completionCache{
		capacity: capacity,
		ttl:      ttl,
		version:  version,
		clock:    clock,
		lru:      list.New(),
		entries:  make(map[string]*list.Element),
	}
}

// key builds the cache key from the caller-provided version, the command
// path, the values of the flags that have been set on the command, and the
// word being completed.
func (c *completionCache) key(cmd *Command, toComplete string) string {
	var sb strings.Builder
	sb.WriteString("version=")
	sb.WriteString(c.version)
	sb.WriteString("\x00path=")
	sb.WriteString(cmd.CommandPath())
	// Visit visits the flags that have been set, in lexicographical order,
	// so the key is stable for a given set of parsed flag values.
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		sb.WriteString("\x00flag=")
		sb.WriteString(flag.Name)
		sb.WriteString("=")
		sb.WriteString(flag.Value.String())
	})
	sb.WriteString("\x00word=")
	sb.WriteString(toComplete)
	return sb.String()
}

func (c *completionCache) get(key string) ([]Completion, ShellCompDirective, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[key]
	if !ok {
		return nil, 0, false
	}
	entry := elem.Value.(*completionCacheEntry)
	if !entry.expiresAt.IsZero() && !c.clock().Before(entry.expiresAt) {
		c.removeElement(elem)
		return nil, 0, false
	}
	c.lru.MoveToFront(elem)
	return entry.completions, entry.directive, true
}

func (c *completionCache) set(key string, completions []Completion, directive ShellCompDirective) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[key]; ok {
		entry := elem.Value.(*completionCacheEntry)
		entry.completions = completions
		entry.directive = directive
		if c.ttl > 0 {
			entry.expiresAt = c.clock().Add(c.ttl)
		}
		c.lru.MoveToFront(elem)
		return
	}

	entry := &completionCacheEntry{
		key:         key,
		completions: completions,
		directive:   directive,
	}
	if c.ttl > 0 {
		entry.expiresAt = c.clock().Add(c.ttl)
	}
	elem := c.lru.PushFront(entry)
	c.entries[key] = elem

	for c.lru.Len() > c.capacity {
		c.removeElement(c.lru.Back())
	}
}

func (c *completionCache) removeElement(elem *list.Element) {
	c.lru.Remove(elem)
	delete(c.entries, elem.Value.(*completionCacheEntry).key)
}
