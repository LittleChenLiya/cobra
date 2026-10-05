// Copyright 2024 The Cobra Authors
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

// FlagMigration declares that a deprecated flag name should be transparently
// migrated to its canonical name when the command is executed.
//
// The migration is applied to the selected command's effective flag set: the
// canonical flag must exist for the selected command (as a local flag or an
// inherited persistent flag) and the deprecated name must not itself be
// registered as a flag. This means a child command's local flag with the same
// name as a parent persistent flag correctly shadows the parent; the migration
// targets the flag the selected command actually sees.
//
// The original flag registration is never modified: only the parsed arguments
// are rewritten, so the command tree and its flag sets are left untouched.
type FlagMigration struct {
	// Old is the deprecated flag name that users may still pass on the command
	// line. It must not be registered as a flag itself.
	Old string
	// New is the canonical flag name that the value is migrated to. It must be
	// a registered flag of the selected command.
	New string
}

// SetFlagMigrations sets the explicit table mapping deprecated flag names to
// their canonical names for this command and its subcommands.
//
// The table is inherited by subcommands unless a subcommand sets its own
// table. Passing no migrations clears the table for this command.
//
// When both a deprecated name and its canonical name are provided on the
// command line, execution is rejected with an error instead of letting the
// last value win. A warning is printed once per deprecated flag that is used.
//
//	rootCmd.SetFlagMigrations(
//	    cobra.FlagMigration{Old: "old-flag", New: "new-flag"},
//	)
func (c *Command) SetFlagMigrations(migrations ...FlagMigration) {
	c.flagMigrations = migrations
	c.flagMigrationsSet = true
}

// FlagMigrations returns the flag migration table applicable to this command.
// The table is inherited from the nearest ancestor that set one.
func (c *Command) FlagMigrations() []FlagMigration {
	for cmd := c; cmd != nil; cmd = cmd.parent {
		if cmd.flagMigrationsSet {
			return cmd.flagMigrations
		}
	}
	return nil
}

// applyFlagMigrations rewrites deprecated flag names to their canonical names
// in args before parsing. It returns the rewritten arguments.
//
// Migrations are only applied when the canonical flag exists in the selected
// command's effective flag set and the deprecated name is not itself a
// registered flag. Flags that are not part of the migration table are left
// untouched.
//
// If both a deprecated name and its canonical name are explicitly provided,
// an error is returned so the conflict is reported before PreRun runs.
func (c *Command) applyFlagMigrations(args []string) ([]string, error) {
	migrations := c.FlagMigrations()
	if len(migrations) == 0 {
		return args, nil
	}

	oldToNew := make(map[string]string, len(migrations))
	newNames := make(map[string]bool, len(migrations))
	for _, m := range migrations {
		if m.Old == "" || m.New == "" || m.Old == m.New {
			continue
		}
		// The canonical flag must be part of the selected command's effective
		// flag set. This respects a child's local flag shadowing a parent
		// persistent flag.
		if c.Flags().Lookup(m.New) == nil {
			continue
		}
		// Do not intercept a name that is still registered as a real flag.
		if c.Flags().Lookup(m.Old) != nil {
			continue
		}
		oldToNew[m.Old] = m.New
		newNames[m.New] = true
	}
	if len(oldToNew) == 0 {
		return args, nil
	}

	rewritten := make([]string, 0, len(args))
	oldUsed := make(map[string]bool)
	newUsed := make(map[string]bool)

	for i := 0; i < len(args); {
		s := args[i]
		i++

		if s == "--" {
			rewritten = append(rewritten, s)
			rewritten = append(rewritten, args[i:]...)
			break
		}

		if len(s) < 2 || s[0] != '-' {
			// Positional argument.
			rewritten = append(rewritten, s)
			continue
		}

		if s[1] == '-' {
			i = c.processLongMigration(s, args, i, &rewritten, oldToNew, newNames, oldUsed, newUsed)
		} else {
			i = c.processShortMigration(s, args, i, &rewritten, newNames, newUsed)
		}
	}

	for old, new := range oldToNew {
		if oldUsed[old] && newUsed[new] {
			return nil, fmt.Errorf("flag --%s and flag --%s cannot be used together; use --%s instead", old, new, new)
		}
	}

	for old := range oldUsed {
		c.Printf("Flag --%s has been deprecated, use --%s instead\n", old, oldToNew[old])
	}

	return rewritten, nil
}

// processLongMigration handles a single long flag token, rewriting it to its
// canonical name when it is a deprecated flag. It returns the index of the
// next argument to process.
func (c *Command) processLongMigration(s string, args []string, i int, rewritten *[]string, oldToNew map[string]string, newNames map[string]bool, oldUsed, newUsed map[string]bool) int {
	name := s[2:]
	value := ""
	hasValue := false
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		value = name[eq+1:]
		name = name[:eq]
		hasValue = true
	}

	if newName, ok := oldToNew[name]; ok {
		// Deprecated flag used: rewrite it to the canonical name.
		oldUsed[name] = true
		if hasValue {
			*rewritten = append(*rewritten, "--"+newName+"="+value)
		} else {
			*rewritten = append(*rewritten, "--"+newName)
			if c.flagTakesValue(newName) && i < len(args) {
				*rewritten = append(*rewritten, args[i])
				i++
			}
		}
		return i
	}

	// The flag is not deprecated; leave it as is and record if the canonical
	// name was explicitly provided.
	if newNames[name] {
		newUsed[name] = true
	}
	*rewritten = append(*rewritten, s)
	if !hasValue && c.flagTakesValue(name) && i < len(args) {
		*rewritten = append(*rewritten, args[i])
		i++
	}
	return i
}

// processShortMigration handles a single short flag cluster. Short flags are
// never migrated (migrations are long-name to long-name), but the values they
// consume must still be skipped so a value that looks like a long flag is not
// mistakenly migrated. It returns the index of the next argument to process.
func (c *Command) processShortMigration(s string, args []string, i int, rewritten *[]string, newNames map[string]bool, newUsed map[string]bool) int {
	*rewritten = append(*rewritten, s)
	cluster := s[1:]

	// Go test shorthand flags (-test.*) are passed through by pflag without
	// consuming any argument.
	if strings.HasPrefix(cluster, "test.") {
		return i
	}

	for len(cluster) > 0 {
		ch := cluster[0]
		cluster = cluster[1:]

		f := c.Flags().ShorthandLookup(string(ch))
		if f == nil {
			// Unknown shorthand: pflag reports an error. Stop processing this
			// cluster; the error will be reported during parsing.
			return i
		}

		// Record if a shorthand for a canonical flag was explicitly provided,
		// so a conflict with the deprecated name can be detected.
		if newNames[f.Name] {
			newUsed[f.Name] = true
		}

		if f.NoOptDefVal != "" {
			// Boolean-like shorthand; continue to the next character.
			continue
		}

		// This shorthand takes a value.
		if len(cluster) > 0 {
			// The value is the rest of the cluster (e.g. -cvalue or -c=value).
			cluster = ""
		} else if i < len(args) {
			// The value is the next argument.
			*rewritten = append(*rewritten, args[i])
			i++
		}
		return i
	}
	return i
}

// flagTakesValue reports whether the named flag consumes a following argument
// as its value. Returns false for boolean-like flags (which have a NoOptDefVal)
// and for unknown flags.
func (c *Command) flagTakesValue(name string) bool {
	f := c.Flags().Lookup(name)
	if f == nil {
		return false
	}
	return f.NoOptDefVal == ""
}
