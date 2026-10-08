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
	"context"
	"encoding/csv"
	"reflect"
	"strings"
	"unsafe"

	flag "github.com/spf13/pflag"
)

// ParseSession allows a long-running program to run several independent
// parsing rounds against the same, already registered command tree.
//
// Every call to Execute starts from the flags' registered default values: no
// Changed flag and no value from a previous round leaks in. The round produces
// an isolated ParseResult and, once the command handlers have run, the complete
// command tree is restored to the state it was in before the session started.
// Sessions may be nested.
type ParseSession struct {
	root *Command
}

// NewParseSession creates a ParseSession bound to the command tree that c
// belongs to.
func NewParseSession(c *Command) *ParseSession {
	return &ParseSession{root: c.Root()}
}

// ParseResult is the isolated outcome of one parsing round. It holds detached
// copies of the parsed flags, so it stays valid after the session restored the
// command tree.
type ParseResult struct {
	// Cmd is the command that was selected for execution.
	Cmd *Command
	// Args are the positional (non-flag) arguments passed to the handler.
	Args []string
	// Err is the error returned by parsing/execution, if any.
	Err error
	// Panic holds a value recovered from a panicking command handler.
	Panic interface{}

	flags *flag.FlagSet
}

// Flags returns the detached FlagSet containing the values that were in effect
// during this parsing round. Reading from it never affects the live command
// tree.
func (r *ParseResult) Flags() *flag.FlagSet {
	return r.flags
}

// Changed reports whether the named flag was explicitly provided during this
// parsing round.
func (r *ParseResult) Changed(name string) bool {
	f := r.flags.Lookup(name)
	return f != nil && f.Changed
}

// Execute runs one isolated parsing round over args (the arguments without the
// program name). The command tree, including every flag value and its Changed
// state, is restored before Execute returns; this also happens when parsing
// fails or a handler panics, in which case the recovered panic value is exposed
// through the ParseResult.
func (s *ParseSession) Execute(args []string) (result *ParseResult) {
	snapshot := newTreeSnapshot(s.root)
	snapshot.capture()
	defer snapshot.restore()

	result = &ParseResult{flags: flag.NewFlagSet(s.root.DisplayName(), flag.ContinueOnError)}
	defer func() {
		if p := recover(); p != nil {
			result.Panic = p
		}
		// Freeze the parsed state while the live tree still holds the values
		// of this round; the snapshot restore runs after this defer.
		snapshot.copyResultFlags(result)
	}()

	// Each round starts from the values the flags were registered with.
	snapshot.resetToDefaults()

	s.root.SetArgs(args)
	if s.root.args == nil {
		// Avoid ExecuteC falling back to os.Args when no args were supplied.
		s.root.args = []string{}
	}

	cmd, err := s.root.ExecuteC()
	result.Cmd = cmd
	result.Err = err
	if cmd != nil {
		result.Args = append(result.Args, cmd.Flags().Args()...)
	}
	return result
}

// flagState captures the mutable state of a single pflag.Flag.
type flagState struct {
	flag *flag.Flag
	// changed is pflag's "was set on the command line" marker.
	changed bool
	// saved is an independent deep copy of the Value exactly as it was at
	// capture time; it is swapped back in on restore.
	saved flag.Value
}

// setParseState captures the mutable per-FlagSet parse state of one FlagSet.
type setParseState struct {
	set           *flag.FlagSet
	parsed        bool
	actual        map[flag.NormalizedName]*flag.Flag
	orderedActual []*flag.Flag
	args          []string
	argsLenAtDash int
}

// commandState captures execution-relevant mutable state of one command.
type commandState struct {
	cmd *Command

	args         []string
	ctx          context.Context
	calledAsName string
	calledAsSet  bool
	commands     []*Command
	helpCommand  *Command
	helpCmdSet   bool
	flagErrorBuf []byte

	// Original flag-set pointers; sets that are lazily materialised during a
	// round (merged/local/inherited caches or default help flags) are swapped
	// back to these references on restore.
	flags   *flag.FlagSet
	pflags  *flag.FlagSet
	lflags  *flag.FlagSet
	iflags  *flag.FlagSet
	parents *flag.FlagSet
}

// treeSnapshot captures every piece of mutable state a parsing round touches.
type treeSnapshot struct {
	root *Command

	commands []*commandState
	sets     []*setParseState
	flags    []*flagState
	seen     map[*flag.Flag]bool
	seenSets map[*flag.FlagSet]bool
	// originalSets lists the FlagSet pointers that existed before the round;
	// only those receive parse-state restoration.
	originalSets map[*flag.FlagSet]bool
}

func newTreeSnapshot(root *Command) *treeSnapshot {
	return &treeSnapshot{
		root:         root,
		seen:         make(map[*flag.Flag]bool),
		seenSets:     make(map[*flag.FlagSet]bool),
		originalSets: make(map[*flag.FlagSet]bool),
	}
}

func (s *treeSnapshot) walk(c *Command, fn func(*Command)) {
	fn(c)
	for _, child := range c.commands {
		s.walk(child, fn)
	}
}

// capture stores the current state of every command, flag set and flag in the
// tree. Merged/local/inherited flag sets are materialised first so that the
// snapshot covers exactly what a parsing round touches. Flags are
// de-duplicated because the derived sets re-share flag pointers.
func (s *treeSnapshot) capture() {
	s.walk(s.root, func(c *Command) {
		// Trigger the same lazy initialisation that Execute/ParseFlags does,
		// without parsing any arguments.
		c.InitDefaultHelpFlag()
		if c.Version != "" {
			c.InitDefaultVersionFlag()
		}
		_ = c.LocalFlags()
		_ = c.InheritedFlags()

		cs := &commandState{
			cmd:          c,
			args:         c.args,
			ctx:          c.ctx,
			calledAsName: c.commandCalledAs.name,
			calledAsSet:  c.commandCalledAs.called,
			commands:     c.commands,
			helpCommand:  c.helpCommand,
			helpCmdSet:   c.helpCommand != nil,
			flags:        c.flags,
			pflags:       c.pflags,
			lflags:       c.lflags,
			iflags:       c.iflags,
			parents:      c.parentsPflags,
		}
		if c.flagErrorBuf != nil {
			cs.flagErrorBuf = append(cs.flagErrorBuf, c.flagErrorBuf.Bytes()...)
		}
		s.commands = append(s.commands, cs)

		for _, fs := range s.ownedSets(c) {
			s.originalSets[fs] = true
			st := &setParseState{
				set:           fs,
				parsed:        fsField(fs, "parsed").Bool(),
				actual:        readActual(fs),
				orderedActual: readOrderedActual(fs),
				argsLenAtDash: fs.ArgsLenAtDash(),
			}
			st.args = append(st.args, fs.Args()...)
			s.sets = append(s.sets, st)

			fs.VisitAll(func(f *flag.Flag) {
				if !s.seen[f] {
					s.seen[f] = true
					s.flags = append(s.flags, &flagState{
						flag:    f,
						changed: f.Changed,
						saved:   cloneValue(f.Value),
					})
				}
			})
		}
	})
}

func (s *treeSnapshot) ownedSets(c *Command) []*flag.FlagSet {
	var sets []*flag.FlagSet
	add := func(fs *flag.FlagSet) {
		if fs != nil && !s.seenSets[fs] {
			s.seenSets[fs] = true
			sets = append(sets, fs)
		}
	}
	add(c.flags)
	add(c.pflags)
	add(c.lflags)
	add(c.iflags)
	add(c.parentsPflags)
	return sets
}

// resetToDefaults returns every captured flag to its registered default value
// and clears its Changed state before a new round starts.
func (s *treeSnapshot) resetToDefaults() {
	for _, st := range s.flags {
		st.flag.Value = defaultValue(st.flag, st.saved)
		// Clear internal slice/map append bookkeeping after seeding the
		// default, so the first parsed occurrence replaces (not appends to)
		// the default value.
		setValueChanged(st.flag.Value, false)
		st.flag.Changed = false
	}
	for _, st := range s.sets {
		clearSetBookkeeping(st.set)
	}
}

// defaultValue returns an independent Value holding a flag's registered
// default. The default is applied on a pristine, fully initialised Value of the
// same type, using the type-appropriate decoding (CSV slices, stringArray
// elements, maps or a plain Set).
func defaultValue(f *flag.Flag, original flag.Value) flag.Value {
	seed := newValue(f.Value)
	if seed == nil {
		return f.Value
	}
	if !seedDefault(seed, f.DefValue) {
		// A custom Value that rejects its own registered default keeps the
		// pristine captured state.
		if dup := cloneValue(original); dup != nil {
			return dup
		}
		return f.Value
	}
	return seed
}

// seedDefault applies a flag's textual default to a freshly initialised Value.
func seedDefault(v flag.Value, defValue string) bool {
	if sv, ok := v.(flag.SliceValue); ok {
		elems, ok := parseDefaultElements(defValue)
		if !ok {
			return false
		}
		setValueChanged(v, false)
		if v.Type() == "stringArray" {
			// stringArray treats commas literally; seed each element through
			// Set on the empty value so it does not become one CSV element.
			setValueChanged(v, true)
			for _, e := range elems {
				if err := v.Set(e); err != nil {
					return false
				}
			}
			setValueChanged(v, false)
			return true
		}
		if err := sv.Replace(elems); err != nil {
			return false
		}
		return true
	}
	return v.Set(defValue) == nil
}

// parseDefaultElements decodes the bracket-wrapped CSV textual default of a
// slice flag into its elements.
func parseDefaultElements(defValue string) ([]string, bool) {
	raw := defValue
	if len(raw) >= 2 && raw[0] == '[' && raw[len(raw)-1] == ']' {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" {
		return []string{}, true
	}
	r := csv.NewReader(strings.NewReader(raw))
	records, err := r.Read()
	if err != nil {
		return nil, false
	}
	return records, true
}

// restore puts the tree back exactly into the captured state.
func (s *treeSnapshot) restore() {
	for _, st := range s.flags {
		// Swap in an independent copy of the captured Value; this preserves
		// internal state (slice/map append bookkeeping included) exactly.
		if dup := cloneValue(st.saved); dup != nil {
			st.flag.Value = dup
		}
		st.flag.Changed = st.changed
	}

	for _, st := range s.sets {
		if s.originalSets[st.set] {
			st.restore()
		}
	}

	for _, cs := range s.commands {
		c := cs.cmd
		c.args = cs.args
		c.ctx = cs.ctx
		c.commandCalledAs.name = cs.calledAsName
		c.commandCalledAs.called = cs.calledAsSet
		c.commands = cs.commands
		if cs.helpCmdSet {
			c.helpCommand = cs.helpCommand
		} else {
			c.helpCommand = nil
		}
		// Swap lazily materialised flag sets back to their original
		// references (nil when they did not exist before the round).
		c.flags = cs.flags
		c.pflags = cs.pflags
		c.lflags = cs.lflags
		c.iflags = cs.iflags
		c.parentsPflags = cs.parents
		if c.flagErrorBuf == nil {
			c.flagErrorBuf = new(bytes.Buffer)
		}
		c.flagErrorBuf.Reset()
		_, _ = c.flagErrorBuf.Write(cs.flagErrorBuf)
	}
}

// copyResultFlags builds detached copies of the flags of the executed command
// and of all its parents, as seen at the end of the round. The executed
// command and its local flags take precedence over inherited flags, mirroring
// the result of c.Flags().
func (s *treeSnapshot) copyResultFlags(result *ParseResult) {
	if result.Cmd == nil {
		return
	}
	var chain []*Command
	for c := result.Cmd; c != nil; c = c.Parent() {
		chain = append(chain, c)
	}
	seen := make(map[string]bool)
	// Child first so local flags shadow inherited ones.
	for _, c := range chain {
		c.Flags().VisitAll(func(f *flag.Flag) {
			if seen[f.Name] {
				return
			}
			seen[f.Name] = true
			if cp := cloneFlag(f); cp != nil {
				result.flags.AddFlag(cp)
			}
		})
	}
}

// cloneFlag returns a detached copy of f backed by an independent Value that
// reproduces the current state of f.
func cloneFlag(f *flag.Flag) *flag.Flag {
	v := cloneValue(f.Value)
	if v == nil {
		return nil
	}
	cp := *f
	cp.Value = v
	if len(f.Annotations) != 0 {
		cp.Annotations = make(map[string][]string, len(f.Annotations))
		for k, vals := range f.Annotations {
			cp.Annotations[k] = append([]string(nil), vals...)
		}
	}
	return &cp
}

// cloneValue returns a new Value of the same dynamic type as src holding an
// independent copy of src's state. State is reproduced through the public
// pflag.Value API (Set, and SliceValue.Replace for slice-style Values), which
// works for both the built-in and any user-defined Value implementations.
func cloneValue(src flag.Value) flag.Value {
	dup := newValue(src)
	if dup == nil {
		return nil
	}
	copyState(dup, src)
	return dup
}

// newValue allocates a zero Value of the same dynamic type as v and makes it
// usable: slice/map Values store their data behind internal pointers that are
// nil on a freshly allocated struct and must be initialised before Set.
func newValue(v flag.Value) flag.Value {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil
	}
	dup := reflect.New(rv.Elem().Type())
	nv, ok := dup.Interface().(flag.Value)
	if !ok {
		return nil
	}
	initInternals(nv)
	return nv
}

// copyState drives dst into the same logical state as src, where both have the
// same dynamic type. Slice-style Values are copied element-wise: stringArray
// values treat commas literally and are fed one Set call per element, while
// CSV slices/maps use Replace. Everything else is replayed through Set.
func copyState(dst, src flag.Value) {
	if d, ok := dst.(flag.SliceValue); ok {
		if s, ok := src.(flag.SliceValue); ok {
			elems := s.GetSlice()
			setValueChanged(dst, false)
			if dst.Type() == "stringArray" {
				// stringArray treats commas literally; feed each element via
				// Set onto the empty value (changed=true makes it append).
				setValueChanged(dst, true)
				for _, elem := range elems {
					if err := dst.Set(elem); err != nil {
						break
					}
				}
			} else {
				_ = d.Replace(elems)
			}
			setValueChanged(dst, valueChanged(src))
			return
		}
	}
	if err := dst.Set(src.String()); err == nil {
		return
	}
	// Some serialisations (maps) are bracket-wrapped while Set expects raw
	// content.
	setValueChanged(dst, false)
	_ = dst.Set(stripBrackets(src.String()))
	setValueChanged(dst, valueChanged(src))
}

// stripBrackets removes a surrounding pair of square brackets.
func stripBrackets(s string) string {
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return s[1 : len(s)-1]
	}
	return s
}

// initInternals ensures nil internal pointer/slice/map fields of a freshly
// allocated slice/map-style Value are usable, and clears append bookkeeping.
func initInternals(v flag.Value) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < elem.NumField(); i++ {
		field := elem.Field(i)
		if !field.CanSet() {
			// Reachable via unsafe for the unexported storage fields.
			field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
		}
		switch field.Kind() {
		case reflect.Ptr:
			if field.IsNil() {
				field.Set(reflect.New(field.Type().Elem()))
			}
		case reflect.Slice:
			if field.IsNil() {
				field.Set(reflect.MakeSlice(field.Type(), 0, 0))
			}
		case reflect.Map:
			if field.IsNil() {
				field.Set(reflect.MakeMap(field.Type()))
			}
		}
	}
	setValueChanged(v, false)
}

// valueChanged reports whether a slice/map-style Value is currently marked
// internally as changed. It returns false for Values without such a field.
func valueChanged(v flag.Value) bool {
	if f := changedField(v); f.IsValid() {
		return f.Bool()
	}
	return false
}

// setValueChanged sets the internal changed bookkeeping used by pflag slice-
// and map-style Values.
func setValueChanged(v flag.Value, changed bool) {
	if f := changedField(v); f.IsValid() {
		f.SetBool(changed)
	}
}

// changedField returns a settable reflect.Value for the internal "changed"
// bool of slice/map-style Values, or an invalid Value otherwise.
func changedField(v flag.Value) reflect.Value {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return reflect.Value{}
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	field := elem.FieldByName("changed")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return reflect.Value{}
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
}

// fsField returns a settable reflect.Value for an unexported FlagSet field.
func fsField(fs *flag.FlagSet, name string) reflect.Value {
	field := reflect.ValueOf(fs).Elem().FieldByName(name)
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
}

func readActual(fs *flag.FlagSet) map[flag.NormalizedName]*flag.Flag {
	v := fsField(fs, "actual")
	if v.IsNil() {
		return nil
	}
	out := make(map[flag.NormalizedName]*flag.Flag, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		out[iter.Key().Interface().(flag.NormalizedName)] = iter.Value().Interface().(*flag.Flag)
	}
	return out
}

func readOrderedActual(fs *flag.FlagSet) []*flag.Flag {
	v := fsField(fs, "orderedActual")
	out := make([]*flag.Flag, v.Len())
	for i := 0; i < v.Len(); i++ {
		out[i] = v.Index(i).Interface().(*flag.Flag)
	}
	return out
}

func clearSetBookkeeping(fs *flag.FlagSet) {
	fsField(fs, "parsed").SetBool(false)
	fsField(fs, "actual").Set(reflect.Zero(fsField(fs, "actual").Type()))
	fsField(fs, "orderedActual").Set(reflect.Zero(fsField(fs, "orderedActual").Type()))
	fsField(fs, "sortedActual").Set(reflect.Zero(fsField(fs, "sortedActual").Type()))
	fsField(fs, "args").Set(reflect.Zero(fsField(fs, "args").Type()))
	fsField(fs, "argsLenAtDash").SetInt(-1)
}

func (st *setParseState) restore() {
	fs := st.set
	fsField(fs, "parsed").SetBool(st.parsed)

	actual := reflect.MakeMapWithSize(fsField(fs, "actual").Type(), len(st.actual))
	for k, f := range st.actual {
		actual.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(f))
	}
	fsField(fs, "actual").Set(actual)

	ordered := reflect.MakeSlice(fsField(fs, "orderedActual").Type(), 0, len(st.orderedActual))
	for _, f := range st.orderedActual {
		ordered = reflect.Append(ordered, reflect.ValueOf(f))
	}
	fsField(fs, "orderedActual").Set(ordered)
	fsField(fs, "sortedActual").Set(reflect.Zero(fsField(fs, "sortedActual").Type()))

	args := reflect.MakeSlice(fsField(fs, "args").Type(), 0, len(st.args))
	for _, a := range st.args {
		args = reflect.Append(args, reflect.ValueOf(a))
	}
	fsField(fs, "args").Set(args)
	fsField(fs, "argsLenAtDash").SetInt(int64(st.argsLenAtDash))
}
