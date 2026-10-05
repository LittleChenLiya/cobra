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
	"fmt"
	"strings"
)

// FlagMigration describes an explicit rename of a flag. The deprecated flag
// name (OldName) is translated into the canonical flag name (NewName) on the
// command for which the migration was registered (and on its subcommands,
// unless the subcommand overrides the flag name with its own local flag).
type FlagMigration struct {
	// OldName is the deprecated flag name that users may still pass.
	OldName string
	// NewName is the canonical flag name that actually receives the value.
	NewName string
}

// SetFlagMigrations registers an explicit migration table of deprecated flag
// names to canonical flag names for this command.
//
// Several deprecated names may map to the same canonical flag name, e.g.
// {"old-a": "new", "old-b": "new"}. If a user provides both a deprecated name
// and its canonical name (or two deprecated names mapping to the same
// canonical flag) in the same invocation, an error is returned before any
// PreRun hook runs; the last value does not silently win.
//
// The migration table is interpreted against the set of flags that is valid
// for the command that ends up being executed: a parent persistent flag which
// is shadowed by a child local flag of the same name is not migrated on the
// child, and the originally registered flag tree is never modified.
//
// When an old flag name is not registered as a migration for the resolved
// command nothing changes: it is treated like any other (unknown) flag.
func (c *Command) SetFlagMigrations(migrations map[string]string) {
	if migrations == nil {
		c.flagMigrations = nil
		return
	}
	c.flagMigrations = make(map[string]string, len(migrations))
	for oldName, newName := range migrations {
		c.flagMigrations[oldName] = newName
	}
}

// AddFlagMigration registers a single deprecated-to-canonical flag name
// mapping for this command. It is a convenience wrapper around
// SetFlagMigrations.
func (c *Command) AddFlagMigration(oldName, newName string) {
	if c.flagMigrations == nil {
		c.flagMigrations = map[string]string{}
	}
	c.flagMigrations[oldName] = newName
}

// FlagMigrations returns a copy of the migration table registered directly on
// this command (ignoring migrations registered on parents). It returns nil if
// no migration has been registered.
func (c *Command) FlagMigrations() map[string]string {
	if len(c.flagMigrations) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.flagMigrations))
	for oldName, newName := range c.flagMigrations {
		out[oldName] = newName
	}
	return out
}

// effectiveFlagMigrations walks the command chain from the resolved command up
// to the root and builds the migration table that applies to c. Mappings
// registered closer to c take precedence over mappings registered higher up
// in the tree.
//
// The table is then filtered against the merged flag set of c: the canonical name
// must exist as a real flag for c and the deprecated name must not exist as a
// real flag for c. This makes shadowing explicit: a child local flag of the
// same name as a parent persistent flag prevents the parent migration from
// firing on the child.
func (c *Command) effectiveFlagMigrations() map[string]string {
	merged := map[string]string{}

	// Collect from root down to c so the table of the resolved command wins.
	var chain []*Command
	for p := c; p != nil; p = p.parent {
		chain = append(chain, p)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for oldName, newName := range chain[i].flagMigrations {
			merged[oldName] = newName
		}
	}

	if len(merged) == 0 {
		return nil
	}

	effective := map[string]string{}
	for oldName, newName := range merged {
		oldNorm := c.normalizeFlagNameForLookup(oldName)
		newNorm := c.normalizeFlagNameForLookup(newName)
		// The canonical flag must be valid for the resolved command.
		if c.Flags().Lookup(string(newNorm)) == nil {
			continue
		}
		// A live flag shadowing the deprecated name always wins, so this
		// migration is not applicable here.
		if c.Flags().Lookup(string(oldNorm)) != nil {
			continue
		}
		effective[string(oldNorm)] = string(newNorm)
	}
	if len(effective) == 0 {
		return nil
	}
	return effective
}

// normalizeFlagNameForLookup applies the command global normalization
// function (if any) so that migration table keys and command line arguments
// are compared using the same normalization pflag applies while parsing.
func (c *Command) normalizeFlagNameForLookup(name string) string {
	if c.globNormFunc != nil {
		return string(c.globNormFunc(c.Flags(), name))
	}
	return name
}

// applyFlagMigrations validates and rewrites args according to the effective
// migration table, returning the arguments that should be handed to pflag.
//
// It performs a single pre-parse pass so that conflicts (deprecated and
// canonical names used together, or several deprecated names mapping to the
// same canonical flag) are discovered before parsing starts and therefore
// before any PreRun hook runs. Every usage of a deprecated flag name emits a
// warning exactly once.
func (c *Command) applyFlagMigrations(args []string) ([]string, error) {
	migrations := c.effectiveFlagMigrations()
	if len(migrations) == 0 {
		return args, nil
	}

	// canonicalSet holds every canonical flag that participates in the
	// migration table, so direct use of the canonical name is tracked too.
	canonicalSet := map[string]struct{}{}
	for _, newName := range migrations {
		canonicalSet[newName] = struct{}{}
	}

	// usedCanonical records the canonical flags that were set and the source
	// that set them first: the empty string means the canonical name itself,
	// otherwise the deprecated name that mapped to it. Under TraverseChildren
	// flags are parsed level by level, so prior usage recorded by ancestors is
	// inherited to still catch cross-level conflicts.
	usedCanonical := map[string]string{}
	for p := c.parent; p != nil; p = p.parent {
		for canonical, source := range p.migrationUsage {
			usedCanonical[canonical] = source
		}
	}
	warned := c.migrationWarned
	if warned == nil {
		warned = map[string]bool{}
	}

	rewritten := make([]string, 0, len(args))

	noteUsage := func(canonical, source string) error {
		if first, ok := usedCanonical[canonical]; ok && first != source {
			firstArg := canonical
			if first != "" {
				firstArg = first
			}
			secondArg := canonical
			if source != "" {
				secondArg = source
			}
			return fmt.Errorf(
				"flags --%s and --%s cannot be specified together: both set the canonical flag --%s",
				firstArg, secondArg, canonical)
		}
		usedCanonical[canonical] = source
		return nil
	}

	emitWarning := func(oldName, newName string) {
		if warned[oldName] {
			return
		}
		warned[oldName] = true
		fmt.Fprintf(c.flagErrorBuf, "Flag --%s has been deprecated, use --%s instead\n", oldName, newName)
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// "--" terminates flag parsing for pflag and for this pass.
		if arg == "--" {
			rewritten = append(rewritten, args[i:]...)
			break
		}

		// Long flag: "--name" or "--name=value".
		if strings.HasPrefix(arg, "--") {
			body := arg[2:]
			name := body
			hasValue := false
			eq := strings.IndexByte(body, '=')
			if eq >= 0 {
				name = body[:eq]
				hasValue = true
			}
			normName := c.normalizeFlagNameForLookup(name)

			if canonical, isMigrated := migrations[normName]; isMigrated {
				if err := noteUsage(canonical, normName); err != nil {
					return nil, err
				}
				emitWarning(normName, canonical)

				if hasValue {
					rewritten = append(rewritten, "--"+canonical+"="+body[eq+1:])
				} else {
					rewritten = append(rewritten, "--"+canonical)
					// A migrated flag without an explicit value consumes the
					// next argument unless the canonical flag is boolean-like.
					if !c.flagHasNoOptDefVal(canonical) && i+1 < len(args) {
						i++
						rewritten = append(rewritten, args[i])
					}
				}
				continue
			}

			// Track the canonical flag when used directly.
			if _, ok := canonicalSet[normName]; ok {
				if err := noteUsage(normName, ""); err != nil {
					return nil, err
				}
			}
			rewritten = append(rewritten, arg)
			continue
		}

		// Short flag token (e.g. "-v", "-ab", "-fvalue", "-f=value"): keep the
		// token untouched but detect whether its first shorthand sets a
		// canonical flag that participates in a migration, so a conflict with
		// a deprecated name is still caught.
		if strings.HasPrefix(arg, "-") && len(arg) >= 2 && arg != "-" {
			if canonical := c.canonicalTouchedByShortArg(arg, canonicalSet); canonical != "" {
				if err := noteUsage(canonical, ""); err != nil {
					return nil, err
				}
			}
			rewritten = append(rewritten, arg)
			continue
		}

		rewritten = append(rewritten, arg)
	}

	c.migrationWarned = warned
	c.migrationUsage = usedCanonical
	return rewritten, nil
}

// flagHasNoOptDefVal reports whether the canonical flag is satisfied without
// a following value argument (e.g. booleans), matching pflag NoOptDefVal.
func (c *Command) flagHasNoOptDefVal(canonical string) bool {
	f := c.Flags().Lookup(canonical)
	return f != nil && f.NoOptDefVal != ""
}

// canonicalTouchedByShortArg reports the canonical flag name set by the first
// shorthand of a short flag token, but only when that canonical flag is the
// target of a registered migration; it returns the empty string otherwise.
func (c *Command) canonicalTouchedByShortArg(arg string, canonicalSet map[string]struct{}) string {
	f := c.Flags().ShorthandLookup(string(arg[1]))
	if f == nil {
		return ""
	}
	if _, ok := canonicalSet[f.Name]; ok {
		return f.Name
	}
	return ""
}
