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
	"unsafe"

	flag "github.com/spf13/pflag"
)

// ParseResult is the isolated outcome of a single ParseSession.
//
// It holds deep copies of the flag values produced by the session's parse,
// so it stays valid and unchanged even after the session has ended and the
// command's flags have been restored to their previous state.
type ParseResult struct {
	flags *flag.FlagSet
	args  []string
}

// Flags returns the flag set holding this session's parsed values.
// All the usual pflag lookup and typed getter methods (GetString, GetInt,
// GetStringSlice, ...) can be used on it.
func (r *ParseResult) Flags() *flag.FlagSet {
	return r.flags
}

// Args returns the positional arguments that remained after flag parsing.
func (r *ParseResult) Args() []string {
	return r.args
}

// Changed reports whether the named flag was set during this session.
func (r *ParseResult) Changed(name string) bool {
	f := r.flags.Lookup(name)
	return f != nil && f.Changed
}

// Lookup returns the parsed Flag with the given name, or nil if there is no
// such flag. It is mostly useful for flags with custom flag.Value types.
func (r *ParseResult) Lookup(name string) *flag.Flag {
	return r.flags.Lookup(name)
}

// sessionFlagState records the pre-session state of a single flag so it can
// be restored verbatim when the session ends.
type sessionFlagState struct {
	flag    *flag.Flag
	value   flag.Value
	changed bool
}

// ParseSession is an independent flag-parsing session for a Command.
//
// It is meant for long-running programs that need to parse several batches
// of arguments against the same command tree without manually resetting
// every flag's Changed state and value in between.
//
// A session works as follows:
//   - When parsing starts, every flag applicable to the command (local,
//     persistent and inherited) is reset to the baseline value captured the
//     first time a session touched it, normally the registered default, and
//     its Changed state is cleared.
//   - The arguments are then parsed with the command's regular parsing
//     logic, so while the session is active the normal flag access methods
//     (cmd.Flags().GetString, cmd.Flags().Lookup, ...) report this
//     session's values. Variables bound with StringVar and friends are not
//     updated; use the flag getters or the ParseResult instead.
//   - Parse returns an isolated ParseResult that keeps the session's values
//     even after the session ends.
//   - End restores every flag's value and Changed state to exactly what it
//     was before the session started. Sessions may be nested; each nested
//     session still starts from the registered defaults and restores the
//     enclosing session's state when it ends.
//
// A ParseSession is not safe for concurrent use, and concurrent sessions
// must not be run against commands that share flags.
type ParseSession struct {
	cmd    *Command
	states []sessionFlagState
	result *ParseResult
	active bool
}

// NewParseSession creates an independent parse session for the command.
// The session parses against the command's full flag set: local flags,
// persistent flags and persistent flags inherited from its parents.
func (c *Command) NewParseSession() *ParseSession {
	return &ParseSession{cmd: c}
}

// Parse resets the command's flags to its registered defaults, parses args
// with the command's regular flag parsing logic and returns an isolated
// result. The session stays active afterwards: the command's flags keep
// exposing this session's values until End is called.
//
// If parsing fails, the session is ended automatically and the command's
// flags are restored before the error is returned.
func (s *ParseSession) Parse(args []string) (*ParseResult, error) {
	s.begin()

	if s.cmd.DisableFlagParsing {
		s.result = &ParseResult{
			flags: s.snapshotFlags(),
			args:  append([]string(nil), args...),
		}
		return s.result, nil
	}

	if err := s.cmd.ParseFlags(args); err != nil {
		s.End()
		return nil, err
	}

	s.result = &ParseResult{
		flags: s.snapshotFlags(),
		args:  append([]string(nil), s.cmd.Flags().Args()...),
	}
	return s.result, nil
}

// Run parses args and, on success, invokes fn with the isolated result.
// The command's flag state is restored when fn returns, even if fn panics;
// the panic is re-thrown after the state has been restored.
func (s *ParseSession) Run(args []string, fn func(*ParseResult) error) error {
	res, err := s.Parse(args)
	if err != nil {
		return err
	}
	defer s.End()
	return fn(res)
}

// Result returns the result of the session's last successful Parse call,
// or nil if Parse has not succeeded yet.
func (s *ParseSession) Result() *ParseResult {
	return s.result
}

// End restores the value and Changed state of every flag touched by the
// session to what it was before the session started. It is safe to call End
// multiple times and to call it after a failed Parse.
func (s *ParseSession) End() {
	if !s.active {
		return
	}
	for _, st := range s.states {
		st.flag.Value = st.value
		st.flag.Changed = st.changed
	}
	s.states = nil
	s.active = false
}

// begin snapshots the current flag state and resets every flag to its
// baseline value with a cleared Changed state.
func (s *ParseSession) begin() {
	if s.active {
		panic("cobra: Parse called on an already active ParseSession")
	}

	c := s.cmd
	c.mergePersistentFlags()
	templates := c.sessionTemplates()

	s.states = s.states[:0]
	c.Flags().VisitAll(func(f *flag.Flag) {
		tmpl, ok := templates[f]
		if !ok {
			// First time a session touches this flag: capture the current
			// value as the baseline every future session will start from.
			tmpl = deepCopyFlagValue(f.Value)
			templates[f] = tmpl
		}
		s.states = append(s.states, sessionFlagState{flag: f, value: f.Value, changed: f.Changed})
		f.Value = deepCopyFlagValue(tmpl)
		f.Changed = false
	})
	s.active = true
}

// snapshotFlags builds an isolated copy of the command's current flag values.
func (s *ParseSession) snapshotFlags() *flag.FlagSet {
	fs := flag.NewFlagSet(s.cmd.DisplayName(), flag.ContinueOnError)
	s.cmd.Flags().VisitAll(func(f *flag.Flag) {
		nf := *f
		nf.Value = deepCopyFlagValue(f.Value)
		fs.AddFlag(&nf)
	})
	return fs
}

// sessionTemplates returns the baseline flag value templates shared by all
// parse sessions of this command tree. They are stored on the root command
// so that nested sessions on parent and child commands use the same
// baselines for shared (inherited) flags.
func (c *Command) sessionTemplates() map[*flag.Flag]flag.Value {
	root := c.Root()
	if root.parseSessionTemplates == nil {
		root.parseSessionTemplates = make(map[*flag.Flag]flag.Value)
	}
	return root.parseSessionTemplates
}

// deepCopyFlagValue returns an independent copy of a flag value. The copy
// has the same concrete type as the original, so type assertions on
// Lookup(name).Value keep working with it.
func deepCopyFlagValue(v flag.Value) flag.Value {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		// Values are virtually always held by pointer so that Set can mutate
		// them; anything else is treated as immutable and shared.
		return v
	}
	cp := reflect.New(rv.Elem().Type())
	cp.Elem().Set(rv.Elem())
	detachPointedData(cp.Elem())
	if nv, ok := cp.Interface().(flag.Value); ok {
		return nv
	}
	return v
}

// detachPointedData replaces pointer-to-slice and pointer-to-map fields of a
// copied struct with copies of their own, so that mutating the copy never
// leaks into the original. This covers pflag's built-in slice and map flag
// values (stringSliceValue, stringToStringValue, ...), which hold their data
// behind an unexported pointer together with internal bookkeeping such as a
// "changed" marker that must not be shared between a session and the
// resting state of the flag.
func detachPointedData(sv reflect.Value) {
	if sv.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < sv.NumField(); i++ {
		f := sv.Field(i)
		if f.Kind() != reflect.Ptr || f.IsNil() {
			continue
		}
		if !f.CanSet() {
			// #nosec G103 -- required to read and replace pflag's unexported
			// value holders; f is always addressable here.
			f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		}
		elem := f.Elem()
		switch elem.Kind() {
		case reflect.Slice:
			data := reflect.MakeSlice(elem.Type(), elem.Len(), elem.Len())
			reflect.Copy(data, elem)
			ptr := reflect.New(elem.Type())
			ptr.Elem().Set(data)
			f.Set(ptr)
		case reflect.Map:
			data := reflect.MakeMapWithSize(elem.Type(), elem.Len())
			iter := elem.MapRange()
			for iter.Next() {
				data.SetMapIndex(iter.Key(), iter.Value())
			}
			ptr := reflect.New(elem.Type())
			ptr.Elem().Set(data)
			f.Set(ptr)
		}
	}
}
