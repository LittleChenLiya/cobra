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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"
)

// ContractVersion is the version of the command contract model produced by
// ExportContract. It is incremented whenever the structure of the model
// changes in a backwards-incompatible way.
const ContractVersion = 1

// UnknownValue is used in the exported contract for any content that cannot
// be determined statically, for example the runtime value of a flag, which
// would require invoking user code (such as a custom pflag.Value) to obtain.
const UnknownValue = "unknown"

// Sources of an effective flag in a FlagContract.
const (
	// FlagSourceLocal marks a flag that is defined on the exported command
	// itself. A local flag shadows any inherited flag with the same name.
	FlagSourceLocal = "local"
	// FlagSourceInherited marks a flag that is inherited from a parent
	// command's persistent flags.
	FlagSourceInherited = "inherited"
)

// FlagGroupType identifies the kind of constraint a flag group expresses.
type FlagGroupType string

// The supported flag group constraint types.
const (
	// FlagGroupMutuallyExclusive means at most one of the flags may be set.
	FlagGroupMutuallyExclusive FlagGroupType = "mutually_exclusive"
	// FlagGroupOneRequired means at least one of the flags must be set.
	FlagGroupOneRequired FlagGroupType = "one_required"
	// FlagGroupRequiredTogether means either all or none of the flags must be set.
	FlagGroupRequiredTogether FlagGroupType = "required_together"
)

// ExportOptions controls which commands are included in an exported contract.
type ExportOptions struct {
	// IncludeHidden includes commands marked as hidden.
	IncludeHidden bool
	// IncludeDeprecated includes commands marked as deprecated.
	IncludeDeprecated bool
}

// FlagContract is the exported, statically-known description of a single
// effective flag of a command.
type FlagContract struct {
	// Name is the long name of the flag.
	Name string `json:"name"`
	// Shorthand is the one-letter shorthand, if any.
	Shorthand string `json:"shorthand,omitempty"`
	// Usage is the help text of the flag.
	Usage string `json:"usage"`
	// Type is the static pflag type name of the flag (for example "string").
	Type string `json:"type"`
	// Default is the static default value captured when the flag was defined.
	Default string `json:"default"`
	// CurrentValue is always UnknownValue: the runtime value of a flag is
	// dynamic content and is never read during export.
	CurrentValue string `json:"current_value"`
	// Required reports whether the flag was marked as required.
	Required bool `json:"required"`
	// Hidden reports whether the flag is hidden from help output.
	Hidden bool `json:"hidden,omitempty"`
	// Deprecated is the deprecation message, if the flag is deprecated.
	Deprecated string `json:"deprecated,omitempty"`
	// NoOptDefVal is the value the flag takes when used without an argument.
	NoOptDefVal string `json:"no_opt_def_val,omitempty"`
	// Source is FlagSourceLocal or FlagSourceInherited.
	Source string `json:"source"`
	// DefinedAt is the canonical command path of the command on which the
	// flag was originally defined, allowing shadowed and inherited flags to
	// be traced back to their origin.
	DefinedAt string `json:"defined_at"`
}

// FlagGroupContract is the exported description of a flag group constraint
// such as the ones created by MarkFlagsRequiredTogether, MarkFlagsOneRequired
// and MarkFlagsMutuallyExclusive.
type FlagGroupContract struct {
	// Type is the kind of constraint the group expresses.
	Type FlagGroupType `json:"type"`
	// Flags are the sorted names of the flags participating in the group.
	Flags []string `json:"flags"`
}

// CommandContract is the exported, statically-known description of a single
// command and, recursively, its exported subcommands.
type CommandContract struct {
	// Name is the canonical name of the command.
	Name string `json:"name"`
	// Path is the canonical command path, for example "root child".
	Path string `json:"path"`
	// Aliases are the alternative names resolving to this canonical command.
	// Aliases are identities only; they never duplicate the subcommand tree.
	Aliases []string `json:"aliases,omitempty"`
	// Use is the one-line usage message, including argument documentation.
	Use string `json:"use"`
	// Short is the short description shown in a parent's help output.
	Short string `json:"short,omitempty"`
	// Long is the long description shown in the command's own help output.
	Long string `json:"long,omitempty"`
	// Example is the examples section of the help output.
	Example string `json:"example,omitempty"`
	// ValidArgs are the statically declared valid positional arguments.
	ValidArgs []string `json:"valid_args,omitempty"`
	// Runnable reports whether the command has a Run or RunE function.
	Runnable bool `json:"runnable"`
	// Hidden reports whether the command is hidden.
	Hidden bool `json:"hidden,omitempty"`
	// Deprecated is the deprecation message, if the command is deprecated.
	Deprecated string `json:"deprecated,omitempty"`
	// Flags are the effective flags of the command: local flags plus
	// inherited flags, with shadowing resolved using Cobra's own semantics.
	Flags []FlagContract `json:"flags,omitempty"`
	// FlagGroups are the flag group constraints applying to this command,
	// including constraints declared on inherited flags.
	FlagGroups []FlagGroupContract `json:"flag_groups,omitempty"`
	// Commands are the exported subcommands, sorted by name.
	Commands []*CommandContract `json:"commands,omitempty"`
}

// Contract is the root of an exported command tree model.
type Contract struct {
	// Version is the contract model version, currently ContractVersion.
	Version int `json:"version"`
	// Root is the exported command the export was started from.
	Root *CommandContract `json:"root"`
}

// ExportContract exports the command tree rooted at c as a versioned,
// statically-known contract model suitable for offline consumption by IDEs
// and documentation tools.
//
// The export is purely static: it never invokes Run, PreRun, Args validators,
// dynamic completion functions or pflag.Value.Set, and it does not modify the
// command tree or any flag state. Flag inheritance and shadowing follow
// Cobra's own semantics. Content that cannot be determined statically is
// reported as UnknownValue.
//
// The output is deterministic: repeated exports of the same tree produce
// identical models. Errors are returned, including the canonical command
// path, for duplicate aliases, cycles in the command tree, and flag groups
// referencing unknown flags.
func (c *Command) ExportContract(opts *ExportOptions) (*Contract, error) {
	if opts == nil {
		opts = &ExportOptions{}
	}
	root, err := exportCommand(c, opts)
	if err != nil {
		return nil, err
	}
	return &Contract{Version: ContractVersion, Root: root}, nil
}

// ExportContractJSON is a convenience wrapper around ExportContract that
// marshals the resulting model to indented JSON. Repeated calls on an
// unmodified tree produce byte-identical output.
func (c *Command) ExportContractJSON(opts *ExportOptions) ([]byte, error) {
	contract, err := c.ExportContract(opts)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(contract, "", "  ")
}

// exportCommand exports a single command and its subcommand tree. The
// command's ancestry must be acyclic, which is verified before any Cobra
// helper that walks the parent chain (such as CommandPath or LocalFlags) is
// called, so a malformed tree cannot cause unbounded recursion.
func exportCommand(c *Command, opts *ExportOptions) (*CommandContract, error) {
	if err := checkAcyclicAncestry(c); err != nil {
		return nil, err
	}
	path := c.CommandPath()

	cc := &CommandContract{
		Name:       c.Name(),
		Path:       path,
		Use:        c.Use,
		Short:      c.Short,
		Long:       c.Long,
		Example:    c.Example,
		Runnable:   c.Runnable(),
		Hidden:     c.Hidden,
		Deprecated: c.Deprecated,
	}
	for _, a := range c.ValidArgs {
		cc.ValidArgs = append(cc.ValidArgs, string(a))
	}

	// Aliases are exported as names pointing at this canonical command only.
	seen := map[string]bool{c.Name(): true}
	for _, alias := range c.Aliases {
		if seen[alias] {
			return nil, fmt.Errorf("cobra: command %q has duplicate alias %q", path, alias)
		}
		seen[alias] = true
		cc.Aliases = append(cc.Aliases, alias)
	}

	flags, err := exportFlags(c, path)
	if err != nil {
		return nil, err
	}
	cc.Flags = flags

	groups, err := exportFlagGroups(c, path)
	if err != nil {
		return nil, err
	}
	cc.FlagGroups = groups

	children := make([]*Command, 0, len(c.Commands()))
	for _, child := range c.Commands() {
		if child.Hidden && !opts.IncludeHidden {
			continue
		}
		if len(child.Deprecated) > 0 && !opts.IncludeDeprecated {
			continue
		}
		children = append(children, child)
	}
	sort.SliceStable(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		exported, err := exportCommand(child, opts)
		if err != nil {
			return nil, err
		}
		cc.Commands = append(cc.Commands, exported)
	}
	return cc, nil
}

// checkAcyclicAncestry walks the parent chain of c and returns an error if
// the chain contains a cycle. It never calls CommandPath, which would itself
// recurse forever on a cyclic chain.
func checkAcyclicAncestry(c *Command) error {
	seen := map[*Command]bool{}
	var names []string
	for cur := c; cur != nil; cur = cur.Parent() {
		names = append([]string{cur.DisplayName()}, names...)
		if seen[cur] {
			return fmt.Errorf("cobra: command tree contains a cycle involving command %q", strings.Join(names, " "))
		}
		seen[cur] = true
	}
	return nil
}

// exportFlags returns the effective flags of c: its local flags and the flags
// inherited from its parents, with local flags shadowing same-named inherited
// ones. It reuses Cobra's own LocalFlags and InheritedFlags so the exported
// view matches runtime behavior exactly.
func exportFlags(c *Command, path string) ([]FlagContract, error) {
	var out []FlagContract
	c.LocalFlags().VisitAll(func(f *flag.Flag) {
		out = append(out, exportFlag(f, FlagSourceLocal, path))
	})
	c.InheritedFlags().VisitAll(func(f *flag.Flag) {
		out = append(out, exportFlag(f, FlagSourceInherited, flagOrigin(c, f)))
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// exportFlag converts a pflag.Flag into its static contract description.
// It only reads static fields and never calls Value.Set or Value.String.
func exportFlag(f *flag.Flag, source, definedAt string) FlagContract {
	fc := FlagContract{
		Name:         f.Name,
		Shorthand:    f.Shorthand,
		Usage:        f.Usage,
		Type:         UnknownValue,
		Default:      f.DefValue,
		CurrentValue: UnknownValue,
		Hidden:       f.Hidden,
		Deprecated:   f.Deprecated,
		NoOptDefVal:  f.NoOptDefVal,
		Source:       source,
		DefinedAt:    definedAt,
	}
	if f.Value != nil {
		// Type is part of the static pflag.Value contract.
		fc.Type = f.Value.Type()
	}
	if required, ok := f.Annotations[BashCompOneRequiredFlag]; ok {
		for _, v := range required {
			if v == "true" {
				fc.Required = true
				break
			}
		}
	}
	return fc
}

// flagOrigin returns the canonical command path of the ancestor on which an
// inherited flag was defined, or UnknownValue if it cannot be determined.
// The caller must have verified that c's ancestry is acyclic.
func flagOrigin(c *Command, f *flag.Flag) string {
	for p := c.Parent(); p != nil; p = p.Parent() {
		if p.PersistentFlags().Lookup(f.Name) == f {
			return p.CommandPath()
		}
	}
	return UnknownValue
}

// exportFlagGroups collects the flag group constraints applying to c. Because
// group annotations live on the flag definitions, constraints declared on
// inherited persistent flags are picked up through c.Flags() as well.
func exportFlagGroups(c *Command, path string) ([]FlagGroupContract, error) {
	flags := c.Flags()
	kinds := []struct {
		annotation string
		groupType  FlagGroupType
	}{
		{requiredAsGroupAnnotation, FlagGroupRequiredTogether},
		{oneRequiredAnnotation, FlagGroupOneRequired},
		{mutuallyExclusiveAnnotation, FlagGroupMutuallyExclusive},
	}

	var groups []FlagGroupContract
	seen := map[string]bool{}
	var exportErr error
	flags.VisitAll(func(f *flag.Flag) {
		if exportErr != nil {
			return
		}
		for _, kind := range kinds {
			for _, group := range f.Annotations[kind.annotation] {
				names := strings.Fields(group)
				for _, name := range names {
					if flags.Lookup(name) == nil {
						exportErr = fmt.Errorf("cobra: command %q has a %s flag group %q referencing unknown flag %q",
							path, kind.groupType, group, name)
						return
					}
				}
				sorted := make([]string, len(names))
				copy(sorted, names)
				sort.Strings(sorted)
				key := string(kind.groupType) + "\x1f" + strings.Join(sorted, " ")
				if !seen[key] {
					seen[key] = true
					groups = append(groups, FlagGroupContract{Type: kind.groupType, Flags: sorted})
				}
			}
		}
	})
	if exportErr != nil {
		return nil, exportErr
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Type != groups[j].Type {
			return groups[i].Type < groups[j].Type
		}
		return strings.Join(groups[i].Flags, " ") < strings.Join(groups[j].Flags, " ")
	})
	return groups, nil
}
