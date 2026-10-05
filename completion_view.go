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
	"bytes"
	"reflect"
	"unsafe"

	flag "github.com/spf13/pflag"
)

// completionView returns a deep, read-only copy of the command tree rooted
// at c. The view is used during shell completion so that parsing flags and
// invoking completion callbacks never mutates the original command tree or
// leaves flag.Changed state that would pollute a subsequent Execute.
//
// The view is constructed from the registered baseline (the original
// command's default flag values). Parsing during completion operates on the
// view's own flag copies and is discarded when completion ends.
func (c *Command) completionView() *Command {
	return c.cloneForCompletion(nil)
}

// cloneForCompletion recursively clones c and all of its children for use as
// a read-only completion view. parent is the view's parent command (nil for
// the root of the view).
func (c *Command) cloneForCompletion(parent *Command) *Command {
	view := *c
	view.parent = parent
	view.readOnly = true
	view.ctx = c.ctx

	// Clone slices so the view does not share backing arrays with the
	// original command.
	view.Aliases = append([]string(nil), c.Aliases...)
	view.SuggestFor = append([]string(nil), c.SuggestFor...)
	view.ValidArgs = append([]Completion(nil), c.ValidArgs...)
	view.ArgAliases = append([]string(nil), c.ArgAliases...)
	view.Annotations = cloneStringMap(c.Annotations)

	// The view is a throwaway object; reset fields that describe runtime
	// state rather than the registered baseline.
	view.args = nil
	view.commandCalledAs = struct {
		name   string
		called bool
	}{}
	view.commandgroups = nil
	view.commands = nil
	view.helpCommand = nil
	view.flags = nil
	view.pflags = nil
	view.lflags = nil
	view.iflags = nil
	view.parentsPflags = nil
	view.flagErrorBuf = nil
	view.commandsAreSorted = false

	// Deep-copy the flag sets so parsing during completion never mutates
	// the original flags.
	view.cloneFlagsFrom(c)

	// Recursively clone children. Skip the temporary __complete command,
	// which is added to the root only during completion and is not part of
	// the registered baseline. Also skip the default help command when it
	// is the only subcommand (besides __complete), since it would otherwise
	// cause legacyArgs to reject arbitrary positional args on a root that
	// originally had no subcommands.
	hasRealSubCommands := false
	for _, sub := range c.commands {
		if sub.Name() != ShellCompRequestCmd && sub != c.helpCommand {
			hasRealSubCommands = true
			break
		}
	}
	for _, sub := range c.commands {
		if sub.Name() == ShellCompRequestCmd {
			continue
		}
		if sub == c.helpCommand && !hasRealSubCommands {
			continue
		}
		subView := sub.cloneForCompletion(&view)
		view.commands = append(view.commands, subView)
		if sub == c.helpCommand {
			view.helpCommand = subView
		}
	}

	return &view
}

// cloneFlagsFrom deep-copies the flag sets of src into view. Each flag's
// Value is cloned so that parsing the view's flags does not affect the
// original. The cloned flags are shared across view.flags, view.pflags, and
// view.parentsPflags (looked up by name) so that parsing via view.Flags()
// updates the same flag objects that InheritedFlags() and LocalFlags()
// visit.
func (view *Command) cloneFlagsFrom(src *Command) {
	// Ensure the source's flag sets are fully populated before cloning.
	src.mergePersistentFlags()

	view.flagErrorBuf = new(bytes.Buffer)

	// Clone the full flag set (local + persistent + inherited).
	view.flags = flag.NewFlagSet(src.DisplayName(), flag.ContinueOnError)
	view.flags.SetOutput(view.flagErrorBuf)
	view.flagOrigins = make(map[*flag.Flag]*flag.Flag)
	src.flags.VisitAll(func(f *flag.Flag) {
		clone := cloneFlag(f)
		view.flags.AddFlag(clone)
		view.flagOrigins[clone] = f
	})

	// Build pflags by looking up the cloned persistent flags in view.flags.
	// This keeps the same flag object shared between view.flags and
	// view.pflags.
	view.pflags = flag.NewFlagSet(src.DisplayName(), flag.ContinueOnError)
	view.pflags.SetOutput(view.flagErrorBuf)
	src.pflags.VisitAll(func(f *flag.Flag) {
		if clone := view.flags.Lookup(f.Name); clone != nil {
			view.pflags.AddFlag(clone)
		}
	})

	// Build parentsPflags the same way so inherited flags are the same
	// objects as in view.flags.
	view.parentsPflags = flag.NewFlagSet(src.DisplayName(), flag.ContinueOnError)
	view.parentsPflags.SetOutput(view.flagErrorBuf)
	src.parentsPflags.VisitAll(func(f *flag.Flag) {
		if clone := view.flags.Lookup(f.Name); clone != nil {
			view.parentsPflags.AddFlag(clone)
		}
	})

	// Clone flag-set-level settings that affect parsing behavior.
	// The interspersed setting has no public getter, so read it via
	// reflection. The normalization function is copied directly.
	if interspersed := reflect.ValueOf(src.flags).Elem().FieldByName("interspersed"); interspersed.IsValid() {
		interspersedVal := interspersed.Bool()
		view.flags.SetInterspersed(interspersedVal)
		view.pflags.SetInterspersed(interspersedVal)
		view.parentsPflags.SetInterspersed(interspersedVal)
	}
	if normalizeFunc := src.flags.GetNormalizeFunc(); normalizeFunc != nil {
		view.flags.SetNormalizeFunc(normalizeFunc)
		view.pflags.SetNormalizeFunc(normalizeFunc)
		view.parentsPflags.SetNormalizeFunc(normalizeFunc)
	}

	// Reset the cached local/inherited flag sets; they will be rebuilt
	// lazily from the cloned sets.
	view.lflags = nil
	view.iflags = nil
}

// cloneFlag creates a deep copy of a pflag.Flag. The Value is cloned so
// that parsing the clone does not affect the original.
func cloneFlag(f *flag.Flag) *flag.Flag {
	return &flag.Flag{
		Name:                f.Name,
		Shorthand:           f.Shorthand,
		Usage:               f.Usage,
		Value:               cloneFlagValue(f.Value, f.DefValue),
		DefValue:            f.DefValue,
		Changed:             false,
		NoOptDefVal:         f.NoOptDefVal,
		Deprecated:          f.Deprecated,
		Hidden:              f.Hidden,
		ShorthandDeprecated: f.ShorthandDeprecated,
		Annotations:         cloneStringSliceMap(f.Annotations),
	}
}

// cloneFlagValue creates a new Value of the same concrete type as v,
// initialized to defValue. If the type cannot be cloned (e.g. a non-pointer
// func value), the original is returned.
func cloneFlagValue(v flag.Value, defValue string) flag.Value {
	if v == nil {
		return nil
	}
	t := reflect.TypeOf(v)
	if t.Kind() != reflect.Ptr {
		// Non-pointer values (e.g. funcValue) are stateless; share them.
		return v
	}
	newV := reflect.New(t.Elem())
	if val, ok := newV.Interface().(flag.Value); ok {
		// For struct-based values (e.g. slice/map flags), the zero value
		// has nil internal pointers. Initialize them so that Set() can
		// store data.
		initValueFields(newV)
		if err := val.Set(defValue); err == nil {
			return val
		}
	}
	return v
}

// initValueFields initializes nil pointer fields in a zero Value struct so
// that Set() can store data. The pflag slice/map value types are structs
// with unexported pointer fields; reflection cannot set unexported fields
// without unsafe.
func initValueFields(v reflect.Value) {
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return
	}
	elem := v.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < elem.NumField(); i++ {
		field := elem.Field(i)
		if field.Kind() == reflect.Ptr && field.IsNil() {
			newField := reflect.New(field.Type().Elem())
			//nolint:gosec // G103: unsafe is used only to initialize unexported pointer fields in cloned pflag values.
			ptr := unsafe.Pointer(field.UnsafeAddr())
			reflect.NewAt(field.Type(), ptr).Elem().Set(newField)
		}
	}
}

// cloneStringMap returns a copy of m.
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	clone := make(map[string]string, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}

// cloneStringSliceMap returns a copy of m.
func cloneStringSliceMap(m map[string][]string) map[string][]string {
	if m == nil {
		return nil
	}
	clone := make(map[string][]string, len(m))
	for k, v := range m {
		clone[k] = append([]string(nil), v...)
	}
	return clone
}
