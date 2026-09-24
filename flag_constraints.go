// Copyright 2013-2023 The Cobra Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law and agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cobra

import (
	"fmt"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"
)

const (
	// MaxFlagConstraintExprSize is the maximum number of expression nodes
	// allowed in a single flag constraint expression.
	MaxFlagConstraintExprSize = 32
	// MaxFlagConstraintRules is the maximum number of flag constraint rules
	// that can be registered on a single command.
	MaxFlagConstraintRules = 16
	// MaxFlagConstraintFlags is the maximum number of distinct flags that may
	// be referenced by all flag constraint rules of a single command.  This
	// bound keeps the satisfiability check used by shell completion cheap.
	MaxFlagConstraintFlags = 8
)

// FlagExpr is a composable boolean expression over flag presence.
// A flag is considered present if it was explicitly set on the command line
// (pflag's Changed state); flags left at their default value are not present.
// Expressions are built with Present, All, Any, Not, Implies and Ref.
type FlagExpr interface {
	eval(ctx *flagConstraintEvalContext) bool
	size() int
	check() error
	collectFlags(into map[string]struct{})
	collectRefs(into []string) []string
}

// Present returns an expression that is true when the named flag was
// explicitly set on the command line.  Note that explicitly passing a flag
// with its default value, e.g. --enabled=false, still counts as present.
func Present(flagName string) FlagExpr {
	return presentExpr(flagName)
}

// All returns an expression that is true when all of the given
// sub-expressions are true.  It requires at least one sub-expression.
func All(exprs ...FlagExpr) FlagExpr {
	return allExpr(exprs)
}

// Any returns an expression that is true when at least one of the given
// sub-expressions is true.  It requires at least one sub-expression.
func Any(exprs ...FlagExpr) FlagExpr {
	return anyExpr(exprs)
}

// Not returns an expression that is true when the given expression is false.
func Not(expr FlagExpr) FlagExpr {
	return notExpr{expr: expr}
}

// Implies returns an expression that is true when the then expression holds
// whenever the cond expression holds.
func Implies(cond, then FlagExpr) FlagExpr {
	return impliesExpr{cond: cond, then: then}
}

// Ref returns an expression that refers to the expression of another flag
// constraint rule registered on the same command.  References are resolved
// before the command executes; unknown references and reference cycles are
// reported as errors.
func Ref(ruleID string) FlagExpr {
	return refExpr(ruleID)
}

type presentExpr string

type allExpr []FlagExpr

type anyExpr []FlagExpr

type notExpr struct{ expr FlagExpr }

type impliesExpr struct{ cond, then FlagExpr }

type refExpr string

// flagConstraintEvalContext carries the state needed to evaluate expressions:
// the presence lookup for bound flags and the resolved rule table for Ref.
type flagConstraintEvalContext struct {
	present func(flagName string) bool
	rules   map[string]FlagExpr
}

func (e presentExpr) eval(ctx *flagConstraintEvalContext) bool { return ctx.present(string(e)) }
func (e presentExpr) size() int                                { return 1 }
func (e presentExpr) check() error {
	if len(e) == 0 {
		return fmt.Errorf("flag constraint expression references an empty flag name")
	}
	return nil
}
func (e presentExpr) collectFlags(into map[string]struct{}) { into[string(e)] = struct{}{} }
func (e presentExpr) collectRefs(into []string) []string    { return into }

func (e allExpr) eval(ctx *flagConstraintEvalContext) bool {
	for _, sub := range e {
		if !sub.eval(ctx) {
			return false
		}
	}
	return true
}
func (e allExpr) size() int { return compoundExprSize([]FlagExpr(e)) }
func (e allExpr) check() error {
	if len(e) == 0 {
		return fmt.Errorf("flag constraint expression All requires at least one sub-expression")
	}
	return checkSubExprs([]FlagExpr(e))
}
func (e allExpr) collectFlags(into map[string]struct{}) { collectSubFlags([]FlagExpr(e), into) }
func (e allExpr) collectRefs(into []string) []string    { return collectSubRefs([]FlagExpr(e), into) }

func (e anyExpr) eval(ctx *flagConstraintEvalContext) bool {
	for _, sub := range e {
		if sub.eval(ctx) {
			return true
		}
	}
	return false
}
func (e anyExpr) size() int { return compoundExprSize([]FlagExpr(e)) }
func (e anyExpr) check() error {
	if len(e) == 0 {
		return fmt.Errorf("flag constraint expression Any requires at least one sub-expression")
	}
	return checkSubExprs([]FlagExpr(e))
}
func (e anyExpr) collectFlags(into map[string]struct{}) { collectSubFlags([]FlagExpr(e), into) }
func (e anyExpr) collectRefs(into []string) []string    { return collectSubRefs([]FlagExpr(e), into) }

func (e notExpr) eval(ctx *flagConstraintEvalContext) bool { return !e.expr.eval(ctx) }
func (e notExpr) size() int                                { return 1 + e.expr.size() }
func (e notExpr) check() error                             { return e.expr.check() }
func (e notExpr) collectFlags(into map[string]struct{})    { e.expr.collectFlags(into) }
func (e notExpr) collectRefs(into []string) []string       { return e.expr.collectRefs(into) }

func (e impliesExpr) eval(ctx *flagConstraintEvalContext) bool {
	return !e.cond.eval(ctx) || e.then.eval(ctx)
}
func (e impliesExpr) size() int    { return 1 + e.cond.size() + e.then.size() }
func (e impliesExpr) check() error { return checkSubExprs([]FlagExpr{e.cond, e.then}) }
func (e impliesExpr) collectFlags(into map[string]struct{}) {
	e.cond.collectFlags(into)
	e.then.collectFlags(into)
}
func (e impliesExpr) collectRefs(into []string) []string {
	return e.then.collectRefs(e.cond.collectRefs(into))
}

func (e refExpr) eval(ctx *flagConstraintEvalContext) bool {
	// Cycles and unknown references are rejected before evaluation.
	return ctx.rules[string(e)].eval(ctx)
}
func (e refExpr) size() int                             { return 1 }
func (e refExpr) check() error                          { return nil }
func (e refExpr) collectFlags(into map[string]struct{}) {}
func (e refExpr) collectRefs(into []string) []string    { return append(into, string(e)) }

func compoundExprSize(subs []FlagExpr) int {
	size := 1
	for _, sub := range subs {
		size += sub.size()
	}
	return size
}

func checkSubExprs(subs []FlagExpr) error {
	for _, sub := range subs {
		if sub == nil {
			return fmt.Errorf("flag constraint expression contains a nil sub-expression")
		}
		if err := sub.check(); err != nil {
			return err
		}
	}
	return nil
}

func collectSubFlags(subs []FlagExpr, into map[string]struct{}) {
	for _, sub := range subs {
		sub.collectFlags(into)
	}
}

func collectSubRefs(subs []FlagExpr, into []string) []string {
	for _, sub := range subs {
		into = sub.collectRefs(into)
	}
	return into
}

// flagConstraintRule is a named flag constraint registered on a command.
type flagConstraintRule struct {
	id   string
	expr FlagExpr
}

// AddFlagConstraint registers a named flag constraint rule on the command.
// The rule is validated before the command runs: if the expression does not
// hold for the flags that were explicitly set, execution fails with an error
// reporting the rule ID and the bound flag names.
//
// Flag names are bound when the command executes: a local flag shadows an
// inherited persistent flag of the same name, otherwise the name binds to the
// inherited flag.  Registration fails if the expression is structurally
// invalid (for example an empty All or Any) or exceeds the MaxFlagConstraint*
// limits.
func (c *Command) AddFlagConstraint(id string, expr FlagExpr) error {
	if id == "" {
		return fmt.Errorf("flag constraint rule ID must not be empty")
	}
	if expr == nil {
		return fmt.Errorf("flag constraint rule %q has a nil expression", id)
	}
	for _, r := range c.flagConstraints {
		if r.id == id {
			return fmt.Errorf("flag constraint rule %q is already registered", id)
		}
	}
	if err := expr.check(); err != nil {
		return fmt.Errorf("flag constraint rule %q is invalid: %w", id, err)
	}
	if size := expr.size(); size > MaxFlagConstraintExprSize {
		return fmt.Errorf("flag constraint rule %q is too large: expression has %d nodes, maximum is %d", id, size, MaxFlagConstraintExprSize)
	}
	if len(c.flagConstraints) >= MaxFlagConstraintRules {
		return fmt.Errorf("too many flag constraint rules: maximum is %d", MaxFlagConstraintRules)
	}
	flags := map[string]struct{}{}
	for _, r := range c.flagConstraints {
		r.expr.collectFlags(flags)
	}
	expr.collectFlags(flags)
	if len(flags) > MaxFlagConstraintFlags {
		return fmt.Errorf("flag constraint rule %q exceeds the limit of %d distinct referenced flags per command", id, MaxFlagConstraintFlags)
	}
	c.flagConstraints = append(c.flagConstraints, flagConstraintRule{id: id, expr: expr})
	return nil
}

// lookupConstraintFlag binds a flag name to the flag visible from this
// command: local flags (including the command's own persistent flags) shadow
// inherited persistent flags of the same name.
func (c *Command) lookupConstraintFlag(name string) *flag.Flag {
	if f := c.LocalFlags().Lookup(name); f != nil {
		return f
	}
	return c.InheritedFlags().Lookup(name)
}

// resolveFlagConstraints validates the rule set registered on the command and
// returns the resolved rule table.  It reports unknown rule references,
// reference cycles, unknown flags and limit violations.
func (c *Command) resolveFlagConstraints() (map[string]FlagExpr, error) {
	rules := make(map[string]FlagExpr, len(c.flagConstraints))
	for _, r := range c.flagConstraints {
		rules[r.id] = r.expr
	}

	// Detect unknown references and reference cycles.
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(c.flagConstraints))
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		expr, ok := rules[id]
		if !ok {
			return fmt.Errorf("flag constraint rule %q references unknown rule %q", path[len(path)-1], id)
		}
		switch state[id] {
		case done:
			return nil
		case inStack:
			cycle := append(append([]string{}, path...), id)
			start := 0
			for i, p := range cycle {
				if p == id {
					start = i
					break
				}
			}
			return fmt.Errorf("circular flag constraint reference: %s", strings.Join(cycle[start:], " -> "))
		}
		state[id] = inStack
		for _, ref := range expr.collectRefs(nil) {
			if err := visit(ref, append(append([]string{}, path...), id)); err != nil {
				return err
			}
		}
		state[id] = done
		return nil
	}
	for _, r := range c.flagConstraints {
		if err := visit(r.id, []string{r.id}); err != nil {
			return nil, err
		}
	}

	// Bind referenced flags and enforce the resolved size limits.
	bound := map[string]*flag.Flag{}
	resolveFlags := func(expr FlagExpr, seen map[string]bool) error {
		for name := range flagSetOf(expr, rules, seen) {
			if _, ok := bound[name]; ok {
				continue
			}
			f := c.lookupConstraintFlag(name)
			if f == nil {
				return fmt.Errorf("flag constraint references unknown flag %q", name)
			}
			bound[name] = f
		}
		return nil
	}
	for _, r := range c.flagConstraints {
		if err := resolveFlags(r.expr, map[string]bool{}); err != nil {
			return nil, fmt.Errorf("flag constraint rule %q: %w", r.id, err)
		}
	}
	if len(bound) > MaxFlagConstraintFlags {
		return nil, fmt.Errorf("flag constraints reference %d distinct flags, maximum is %d", len(bound), MaxFlagConstraintFlags)
	}
	return rules, nil
}

// flagSetOf collects the flag names referenced by an expression, expanding
// rule references (which are guaranteed acyclic at this point).
func flagSetOf(expr FlagExpr, rules map[string]FlagExpr, seen map[string]bool) map[string]struct{} {
	out := map[string]struct{}{}
	expr.collectFlags(out)
	for _, ref := range expr.collectRefs(nil) {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if target, ok := rules[ref]; ok {
			for name := range flagSetOf(target, rules, seen) {
				out[name] = struct{}{}
			}
		}
	}
	return out
}

// ValidateFlagConstraints validates the flag constraint rules registered with
// AddFlagConstraint against the flags that were explicitly set.  It returns an
// error describing every violated rule, in rule declaration order, with the
// names of the bound flags involved in each rule.
func (c *Command) ValidateFlagConstraints() error {
	if c.DisableFlagParsing || len(c.flagConstraints) == 0 {
		return nil
	}
	rules, err := c.resolveFlagConstraints()
	if err != nil {
		return err
	}
	ctx := &flagConstraintEvalContext{
		present: func(name string) bool {
			f := c.lookupConstraintFlag(name)
			return f != nil && f.Changed
		},
		rules: rules,
	}
	var violations []string
	for _, r := range c.flagConstraints {
		if r.expr.eval(ctx) {
			continue
		}
		names := make([]string, 0, MaxFlagConstraintFlags)
		for name := range flagSetOf(r.expr, rules, map[string]bool{}) {
			if f := c.lookupConstraintFlag(name); f != nil {
				names = append(names, f.Name)
			}
		}
		sort.Strings(names)
		violations = append(violations, fmt.Sprintf("rule %q is not satisfied by flags [%s]", r.id, strings.Join(names, " ")))
	}
	if len(violations) > 0 {
		return fmt.Errorf("flag constraint %s", strings.Join(violations, "; "))
	}
	return nil
}

// flagConstraintSatisfiableWith reports whether the flag constraint rules of
// the command can still be fully satisfied if the candidate flag is marked
// present, given the flags that are already set.  Flags that have not been
// passed yet remain free: they may still be supplied later, so they are never
// treated as fixed absences.  Flags not referenced by any rule do not affect
// satisfiability.  It is used by shell completion to filter out flag
// candidates that could never lead to a valid combination.
func (c *Command) flagConstraintSatisfiableWith(candidate string) bool {
	if c.DisableFlagParsing || len(c.flagConstraints) == 0 {
		return true
	}
	rules, err := c.resolveFlagConstraints()
	if err != nil {
		// Do not let broken rules break completion.
		return true
	}
	allFlags := map[string]struct{}{}
	for _, r := range c.flagConstraints {
		for name := range flagSetOf(r.expr, rules, map[string]bool{}) {
			allFlags[name] = struct{}{}
		}
	}
	if _, ok := allFlags[candidate]; !ok {
		return true
	}

	fixed := map[string]bool{}
	var free []string
	for name := range allFlags {
		isSet := false
		if f := c.lookupConstraintFlag(name); f != nil {
			isSet = f.Changed
		}
		if isSet || name == candidate {
			fixed[name] = true
		} else {
			free = append(free, name)
		}
	}

	// The number of referenced flags is bounded by MaxFlagConstraintFlags,
	// so this exhaustive search stays cheap.
	ctx := &flagConstraintEvalContext{rules: rules}
	for mask := 0; mask < (1 << uint(len(free))); mask++ {
		assignment := make(map[string]bool, len(allFlags))
		for name, v := range fixed {
			assignment[name] = v
		}
		for i, name := range free {
			assignment[name] = mask&(1<<uint(i)) != 0
		}
		ctx.present = func(name string) bool { return assignment[name] }
		satisfied := true
		for _, r := range c.flagConstraints {
			if !r.expr.eval(ctx) {
				satisfied = false
				break
			}
		}
		if satisfied {
			return true
		}
	}
	return false
}
