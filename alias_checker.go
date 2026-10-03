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
)

// AliasKind identifies how a name token is declared by a command.
type AliasKind uint8

const (
	// AliasKindName is the command's real name (the first word of Use).
	AliasKindName AliasKind = iota
	// AliasKindAlias is a token declared in the command's Aliases slice.
	AliasKindAlias
	// AliasKindSuggestion is a token declared in the command's SuggestFor
	// slice; it only suggests the command but otherwise behaves like an
	// alias for the purpose of name resolution.
	AliasKindSuggestion
)

// String returns the name of the kind: "name", "alias", or "suggestion".
func (k AliasKind) String() string {
	switch k {
	case AliasKindAlias:
		return "alias"
	case AliasKindSuggestion:
		return "suggestion"
	default:
		return "name"
	}
}

// pair returns a name for the combination of two kinds, ordered
// name < alias < suggestion, e.g. "name/alias" or "alias/suggestion".
func (k AliasKind) pair(other AliasKind) string {
	a, b := k, other
	if a > b {
		a, b = b, a
	}
	switch a {
	case AliasKindName:
		switch b {
		case AliasKindAlias:
			return "name/alias"
		case AliasKindSuggestion:
			return "name/suggestion"
		default:
			return "name"
		}
	case AliasKindAlias:
		switch b {
		case AliasKindSuggestion:
			return "alias/suggestion"
		default:
			return "alias"
		}
	default:
		return "suggestion"
	}
}

// AliasOccurrence is a single declaration of a name token by a command.
type AliasOccurrence struct {
	// Command is the command declaring the token.
	Command *Command
	// Kind identifies how the token is declared.
	Kind AliasKind
	// Index is the position of the token within its declaration list
	// (0 for the name, otherwise the index in Aliases or SuggestFor).
	Index int
}

// token returns the declared name token.
func (o AliasOccurrence) token() string {
	switch o.Kind {
	case AliasKindAlias:
		return o.Command.Aliases[o.Index]
	case AliasKindSuggestion:
		return o.Command.SuggestFor[o.Index]
	default:
		return o.Command.Name()
	}
}

// AliasConflict describes a single name or alias collision within the scope
// of one parent command.
type AliasConflict struct {
	// Parent is the command under which the collision was found.
	Parent *Command
	// Token is the name string declared more than once.
	Token string
	// First is the earlier-declared occurrence.
	First AliasOccurrence
	// Second is the later-declared occurrence.
	Second AliasOccurrence
}

// Type returns a short description of the conflict type, for example
// "name/alias conflict" or "duplicate alias declaration".
func (c AliasConflict) Type() string {
	pair := c.First.Kind.pair(c.Second.Kind)
	if c.First.Command == c.Second.Command {
		return "duplicate " + pair + " declaration"
	}
	return pair + " conflict"
}

// String returns a human-readable report of the conflict, including the
// full paths of the commands involved.
func (c AliasConflict) String() string {
	if c.First.Command == c.Second.Command {
		return fmt.Sprintf("%s: %s %q in %q",
			c.Parent.CommandPath(), c.Type(), c.Token, c.First.Command.CommandPath())
	}
	return fmt.Sprintf("%s: %s %q of %q conflicts with %s %q of %q",
		c.Parent.CommandPath(),
		c.First.Kind, c.Token, c.First.Command.CommandPath(),
		c.Second.Kind, c.Token, c.Second.Command.CommandPath())
}

// CheckAliases statically checks the command tree rooted at c for name and
// alias conflicts.
//
// A conflict is reported for every name token (the command's real name, an
// Aliases entry, or a SuggestFor entry) that is declared more than once
// within the scope of a single parent command:
//
//   - two different commands declare the same token under the same parent
//     (for example one command has alias "s" and another is named "s");
//   - the same command declares the same token twice (for example Aliases
//     containing a duplicate entry, or an alias equal to the command's own
//     name).
//
// Tokens that only collide across different parent branches are not
// conflicts, and hidden commands participate in the check.
//
// The check never executes commands, never calls Find (so no argument
// parsing is triggered), and never modifies the command tree.
//
// Conflicts are returned in registration order: for each parent command,
// children are considered in the order they were added, and parent scopes
// are checked depth-first before their children.
func (c *Command) CheckAliases() []AliasConflict {
	var conflicts []AliasConflict
	c.checkAliases(&conflicts)
	return conflicts
}

func (c *Command) checkAliases(conflicts *[]AliasConflict) {
	// Collect every name/alias/suggestion declaration of the direct
	// children, in registration order. The commands slice is used directly
	// (instead of Commands()) so that registration order is preserved.
	occurrences := make([]AliasOccurrence, 0, len(c.commands))
	for _, cmd := range c.commands {
		occurrences = append(occurrences, AliasOccurrence{Command: cmd, Kind: AliasKindName})
		for i := range cmd.Aliases {
			occurrences = append(occurrences, AliasOccurrence{Command: cmd, Kind: AliasKindAlias, Index: i})
		}
		for i := range cmd.SuggestFor {
			occurrences = append(occurrences, AliasOccurrence{Command: cmd, Kind: AliasKindSuggestion, Index: i})
		}
	}

	// Compare every pair of declarations. Iterating in order keeps the
	// report deterministic and aligned with registration order.
	for i := 0; i < len(occurrences); i++ {
		for j := i + 1; j < len(occurrences); j++ {
			first, second := occurrences[i], occurrences[j]
			tokenFirst, tokenSecond := first.token(), second.token()
			if tokenFirst == "" || tokenSecond == "" || !commandNameMatches(tokenFirst, tokenSecond) {
				continue
			}
			*conflicts = append(*conflicts, AliasConflict{
				Parent: c,
				Token:  tokenFirst,
				First:  first,
				Second: second,
			})
		}
	}

	// Recurse into the children, depth-first, in registration order.
	for _, cmd := range c.commands {
		cmd.checkAliases(conflicts)
	}
}
