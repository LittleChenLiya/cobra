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

	"github.com/spf13/pflag"
)

// ErrCompletionViewReadOnly is returned by every mutating method of a
// [CompletionView].  A completion view is an explicit read-only snapshot of a
// command: dynamic completion callbacks may inspect the parsed arguments and
// the effective flags, but they may not modify the command tree, change flag
// values, or run the command.
var ErrCompletionViewReadOnly = errors.New("cobra: completion view is read-only")

// CompletionFlag describes the effective state of a single flag at the moment
// a dynamic completion callback is invoked.
type CompletionFlag struct {
	// Name is the long name of the flag.
	Name string
	// Shorthand is the one-letter shorthand of the flag, if any.
	Shorthand string
	// Usage is the usage message of the flag.
	Usage string
	// Value is the current (completion-time) value of the flag.
	Value string
	// Changed reports whether the flag was set on the command-line being
	// completed (as opposed to holding its registered default value).
	Changed bool
}

// CompletionView is a read-only view of the command for which dynamic
// completion is being performed.
//
// The view is built from the registered baseline of the command tree: cobra
// snapshots every flag before parsing the completion command-line and restores
// that snapshot when completion ends, whether it ends normally or with an
// error.  A CompletionView therefore lets a callback observe the parsed
// arguments and the effective (possibly temporary) flags, while guaranteeing
// that none of that state leaks into a later Execute.  The business Run
// function of the command is never executed as part of completion.
//
// All mutating operations return [ErrCompletionViewReadOnly].
type CompletionView struct {
	cmd        *Command
	args       []string
	toComplete string
	flags      []CompletionFlag
	flagIndex  map[string]int
}

// CompletionViewFunc is a completion callback that receives a read-only
// [CompletionView] instead of the mutable *Command.  The parsed arguments are
// available through [CompletionView.Args] and the partially-typed last
// argument through [CompletionView.ToComplete].
//
// It can be used with [Command.ValidArgsViewFunction] and
// [Command.RegisterFlagCompletionViewFunc].
type CompletionViewFunc func(view *CompletionView) ([]Completion, ShellCompDirective)

// newCompletionView builds a read-only view of cmd after the completion
// command-line has been parsed.
func newCompletionView(cmd *Command, args []string, toComplete string) *CompletionView {
	v := &CompletionView{
		cmd:        cmd,
		args:       append([]string(nil), args...),
		toComplete: toComplete,
		flagIndex:  map[string]int{},
	}
	// Make sure inherited flags are part of the effective flag set, even for
	// commands that disabled flag parsing.
	cmd.mergePersistentFlags()
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if _, exists := v.flagIndex[f.Name]; exists {
			return
		}
		v.flagIndex[f.Name] = len(v.flags)
		v.flags = append(v.flags, CompletionFlag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Usage:     f.Usage,
			Value:     f.Value.String(),
			Changed:   f.Changed,
		})
	})
	return v
}

// CommandName returns the name of the command being completed.
func (v *CompletionView) CommandName() string {
	return v.cmd.Name()
}

// CommandPath returns the full path of the command being completed.
func (v *CompletionView) CommandPath() string {
	return v.cmd.CommandPath()
}

// CalledAs returns the name or alias that was used to reach the command being
// completed, if it is known.
func (v *CompletionView) CalledAs() string {
	return v.cmd.CalledAs()
}

// Args returns the parsed non-flag arguments that precede the argument being
// completed.
func (v *CompletionView) Args() []string {
	return append([]string(nil), v.args...)
}

// ToComplete returns the partially-typed last argument for which completion
// candidates are requested.
func (v *CompletionView) ToComplete() string {
	return v.toComplete
}

// Context returns the context of the completion request.  If the context is
// cancelled, cobra stops publishing completion results, so callbacks should
// abandon any expensive candidate generation.
func (v *CompletionView) Context() context.Context {
	return v.cmd.Context()
}

// Flags returns the effective flags of the command being completed, including
// inherited ones, in the state produced by parsing the completion
// command-line.
func (v *CompletionView) Flags() []CompletionFlag {
	return append([]CompletionFlag(nil), v.flags...)
}

// LookupFlag returns the effective state of the named flag and whether the
// command knows such a flag.
func (v *CompletionView) LookupFlag(name string) (CompletionFlag, bool) {
	idx, ok := v.flagIndex[name]
	if !ok {
		return CompletionFlag{}, false
	}
	return v.flags[idx], true
}

// Flag returns the effective value of the named flag, or the empty string if
// the command does not know such a flag.
func (v *CompletionView) Flag(name string) string {
	if f, ok := v.LookupFlag(name); ok {
		return f.Value
	}
	return ""
}

// FlagChanged reports whether the named flag was set on the completion
// command-line.
func (v *CompletionView) FlagChanged(name string) bool {
	if f, ok := v.LookupFlag(name); ok {
		return f.Changed
	}
	return false
}

// SetFlag always fails: a completion view is read-only.
func (v *CompletionView) SetFlag(name, value string) error {
	return fmt.Errorf("SetFlag(%q, %q): %w", name, value, ErrCompletionViewReadOnly)
}

// AddCommand always fails: a completion view is read-only.
func (v *CompletionView) AddCommand(cmds ...*Command) error {
	return fmt.Errorf("AddCommand(%d commands): %w", len(cmds), ErrCompletionViewReadOnly)
}

// Execute always fails: a completion view is read-only and the business Run
// function of a command is never executed during completion.
func (v *CompletionView) Execute() error {
	return fmt.Errorf("Execute: %w", ErrCompletionViewReadOnly)
}

// completionFlagState captures the baseline state of a single flag so that
// completion-time parsing can be rolled back.
type completionFlagState struct {
	value   string
	changed bool
	isSlice bool
	slice   []string
}

// snapshotCompletionFlags records the current state of every flag reachable
// from root (and its sub-commands) into states.  Flags already present in
// states are left untouched, which allows taking a second snapshot after new
// flags (such as the default --help flag) have been added.
func snapshotCompletionFlags(root *Command, states map[*pflag.Flag]*completionFlagState) {
	record := func(f *pflag.Flag) {
		if _, exists := states[f]; exists {
			return
		}
		st := &completionFlagState{value: f.Value.String(), changed: f.Changed}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			st.isSlice = true
			st.slice = append([]string(nil), sv.GetSlice()...)
		}
		states[f] = st
	}
	var walk func(cmd *Command)
	walk = func(cmd *Command) {
		cmd.Flags().VisitAll(record)
		cmd.PersistentFlags().VisitAll(record)
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// restoreCompletionFlags rolls every flag in states back to its recorded
// baseline.  It is best-effort: values that fail to be restored keep their
// completion-time value, but the Changed marker is always reset.
func restoreCompletionFlags(states map[*pflag.Flag]*completionFlagState) {
	for f, st := range states {
		if st.isSlice {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(st.slice)
			}
		} else {
			_ = f.Value.Set(st.value)
		}
		f.Changed = st.changed
	}
}

// Global map of flag completion view functions.  It is protected by the same
// flagCompletionMutex as flagCompletionFunctions.
var flagCompletionViewFunctions = map[*pflag.Flag]CompletionViewFunc{}

// RegisterFlagCompletionViewFunc should be called to register a function to
// provide completion for a flag, receiving a read-only [CompletionView]
// instead of the mutable *Command.
//
// If both a [CompletionFunc] (see [Command.RegisterFlagCompletionFunc]) and a
// [CompletionViewFunc] are registered for the same flag, the view function
// takes precedence during completion.
func (c *Command) RegisterFlagCompletionViewFunc(flagName string, f CompletionViewFunc) error {
	flag := c.Flag(flagName)
	if flag == nil {
		return fmt.Errorf("RegisterFlagCompletionViewFunc: flag '%s' does not exist", flagName)
	}
	flagCompletionMutex.Lock()
	defer flagCompletionMutex.Unlock()

	if _, exists := flagCompletionViewFunctions[flag]; exists {
		return fmt.Errorf("RegisterFlagCompletionViewFunc: flag '%s' already registered", flagName)
	}
	flagCompletionViewFunctions[flag] = f
	return nil
}

// GetFlagCompletionViewFunc returns the view completion function for the
// given flag of the command, if available.
func (c *Command) GetFlagCompletionViewFunc(flagName string) (CompletionViewFunc, bool) {
	flag := c.Flag(flagName)
	if flag == nil {
		return nil, false
	}

	flagCompletionMutex.RLock()
	defer flagCompletionMutex.RUnlock()

	completionFunc, exists := flagCompletionViewFunctions[flag]
	return completionFunc, exists
}
