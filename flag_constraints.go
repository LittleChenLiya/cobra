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
	"fmt"
	"strings"

	flag "github.com/spf13/pflag"
)

const (
	// MaxFlagConstraintSize is the maximum number of nodes allowed in a single
	// flag constraint expression. AddFlagConstraint rejects larger expressions
	// so that validation and shell completion stay cheap and bounded.
	MaxFlagConstraintSize = 32

	// maxResolvedConstraintSize bounds the size of an expression after
	// constraint references have been expanded.
	maxResolvedConstraintSize = 256

	// maxConstraintCompletionVars bounds the number of distinct flags over
	// which shell completion runs its satisfiability check, keeping the
	// search firmly bounded.
	maxConstraintCompletionVars = 20
)

type constraintKind int

const (
	constraintPresent constraintKind = iota
	constraintAll
	constraintAny
	constraintNot
	constraintImplies
	constraintRef
)

// ConstraintExpr is a composable boolean expression over the presence of
// flags. Leaf expressions created with Present are evaluated against the
// pflag Changed state of the flag they bind to: a flag that was explicitly
// provided on the command line (including an explicit false such as
// --enabled=false) counts as present, while a flag that only has its
// default value does not.
//
// Expressions are registered on a command with AddFlagConstraint and are
// resolved and validated before the command runs.
type ConstraintExpr struct {
	kind     constraintKind
	flagName string
	refID    string
	children []*ConstraintExpr
}

// Present returns an expression that is true when the named flag was
// explicitly set on the command line.
func Present(flagName string) *ConstraintExpr {
	return &ConstraintExpr{kind: constraintPresent, flagName: flagName}
}

// All returns an expression that is true when all of the given
// sub-expressions are true. It requires at least one sub-expression.
func All(exprs ...*ConstraintExpr) *ConstraintExpr {
	return &ConstraintExpr{kind: constraintAll, children: exprs}
}

// Any returns an expression that is true when at least one of the given
// sub-expressions is true. It requires at least one sub-expression.
func Any(exprs ...*ConstraintExpr) *ConstraintExpr {
	return &ConstraintExpr{kind: constraintAny, children: exprs}
}

// Not returns an expression that is true when the given sub-expression is
// false.
func Not(expr *ConstraintExpr) *ConstraintExpr {
	return &ConstraintExpr{kind: constraintNot, children: []*ConstraintExpr{expr}}
}

// Implies returns an expression that is true unless the condition is true
// and the consequence is false.
func Implies(condition, consequence *ConstraintExpr) *ConstraintExpr {
	return &ConstraintExpr{kind: constraintImplies, children: []*ConstraintExpr{condition, consequence}}
}

// RuleRef returns an expression that expands to the expression of the
// constraint rule registered with the given ID. References are resolved
// before the command executes; unknown references and reference cycles are
// reported as errors.
func RuleRef(ruleID string) *ConstraintExpr {
	return &ConstraintExpr{kind: constraintRef, refID: ruleID}
}

// flagConstraintRule is a named constraint expression registered on a command.
type flagConstraintRule struct {
	id   string
	expr *ConstraintExpr
}

// AddFlagConstraint registers a named flag constraint expression on the
// command. The rule is validated before the command runs, and rules
// registered on parent commands also apply to their children, with flag
// names binding to the flags actually visible from the executed command
// (local flags shadow inherited persistent flags of the same name).
//
// An error is returned if the rule ID is empty or already registered on
// this command, if the expression is malformed (nil sub-expressions, empty
// All or Any constructs, empty flag or reference names), or if the
// expression exceeds MaxFlagConstraintSize nodes.
func (c *Command) AddFlagConstraint(ruleID string, expr *ConstraintExpr) error {
	if ruleID == "" {
		return fmt.Errorf("flag constraint rule ID must not be empty")
	}
	for _, r := range c.flagConstraints {
		if r.id == ruleID {
			return fmt.Errorf("flag constraint %q is already registered", ruleID)
		}
	}
	if err := validateConstraintExpr(ruleID, expr); err != nil {
		return err
	}
	c.flagConstraints = append(c.flagConstraints, flagConstraintRule{id: ruleID, expr: expr})
	return nil
}

// validateConstraintExpr checks the structural validity of an expression
// and enforces the size limit.
func validateConstraintExpr(ruleID string, expr *ConstraintExpr) error {
	if expr == nil {
		return fmt.Errorf("flag constraint %q must not be nil", ruleID)
	}
	nodes := 0
	var walk func(e *ConstraintExpr) error
	walk = func(e *ConstraintExpr) error {
		if e == nil {
			return fmt.Errorf("flag constraint %q contains a nil sub-expression", ruleID)
		}
		nodes++
		if nodes > MaxFlagConstraintSize {
			return fmt.Errorf("flag constraint %q exceeds the maximum expression size of %d nodes", ruleID, MaxFlagConstraintSize)
		}
		switch e.kind {
		case constraintPresent:
			if e.flagName == "" {
				return fmt.Errorf("flag constraint %q contains a Present expression with an empty flag name", ruleID)
			}
		case constraintRef:
			if e.refID == "" {
				return fmt.Errorf("flag constraint %q contains a reference with an empty rule ID", ruleID)
			}
		case constraintAll:
			if len(e.children) == 0 {
				return fmt.Errorf("flag constraint %q contains an All expression without sub-expressions", ruleID)
			}
		case constraintAny:
			if len(e.children) == 0 {
				return fmt.Errorf("flag constraint %q contains an Any expression without sub-expressions", ruleID)
			}
		case constraintNot:
			if len(e.children) != 1 {
				return fmt.Errorf("flag constraint %q contains a Not expression with %d sub-expressions", ruleID, len(e.children))
			}
		case constraintImplies:
			if len(e.children) != 2 {
				return fmt.Errorf("flag constraint %q contains an Implies expression with %d sub-expressions", ruleID, len(e.children))
			}
		default:
			return fmt.Errorf("flag constraint %q contains an unknown expression kind", ruleID)
		}
		for _, child := range e.children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(expr)
}

// boundConstraintExpr is a constraint expression whose references have been
// expanded and whose Present leaves have been bound to actual flags.
type boundConstraintExpr struct {
	kind     constraintKind
	flag     *flag.Flag
	children []*boundConstraintExpr
}

// resolvedConstraintRule is a rule ready for evaluation.
type resolvedConstraintRule struct {
	id        string
	expr      *boundConstraintExpr
	flagNames []string
	flags     []*flag.Flag
}

// eval evaluates the expression using the given presence function.
func (e *boundConstraintExpr) eval(present func(*flag.Flag) bool) bool {
	switch e.kind {
	case constraintPresent:
		return present(e.flag)
	case constraintAll:
		for _, child := range e.children {
			if !child.eval(present) {
				return false
			}
		}
		return true
	case constraintAny:
		for _, child := range e.children {
			if child.eval(present) {
				return true
			}
		}
		return false
	case constraintNot:
		return !e.children[0].eval(present)
	case constraintImplies:
		return !e.children[0].eval(present) || e.children[1].eval(present)
	}
	return false
}

// describe renders the expression with the current presence value of each
// leaf, for use in violation messages.
func (e *boundConstraintExpr) describe(present func(*flag.Flag) bool) string {
	switch e.kind {
	case constraintPresent:
		return fmt.Sprintf("present(%s)=%t", e.flag.Name, present(e.flag))
	case constraintNot:
		return "not(" + e.children[0].describe(present) + ")"
	case constraintImplies:
		return "implies(" + e.children[0].describe(present) + ", " + e.children[1].describe(present) + ")"
	case constraintAll, constraintAny:
		op := "all"
		if e.kind == constraintAny {
			op = "any"
		}
		parts := make([]string, len(e.children))
		for i, child := range e.children {
			parts[i] = child.describe(present)
		}
		return op + "(" + strings.Join(parts, ", ") + ")"
	}
	return "unknown"
}

// flagConstraintRules returns the rules applying to this command: rules
// registered on parents (starting at the root) followed by the command's
// own rules, each in registration order.
func (c *Command) flagConstraintRules() []flagConstraintRule {
	var chain []*Command
	for p := c; p != nil; p = p.Parent() {
		chain = append([]*Command{p}, chain...)
	}
	var rules []flagConstraintRule
	for _, cmd := range chain {
		rules = append(rules, cmd.flagConstraints...)
	}
	return rules
}

// resolveFlagConstraints expands references and binds every Present leaf to
// the flag visible from this command, applying the usual shadowing rules
// (local flags shadow inherited persistent flags of the same name).
func (c *Command) resolveFlagConstraints() ([]resolvedConstraintRule, error) {
	if c.DisableFlagParsing {
		return nil, nil
	}
	rules := c.flagConstraintRules()
	if len(rules) == 0 {
		return nil, nil
	}

	// References resolve to the nearest command registering the ID.
	byID := make(map[string]*flagConstraintRule)
	for p := c; p != nil; p = p.Parent() {
		for i := range p.flagConstraints {
			r := &p.flagConstraints[i]
			if _, found := byID[r.id]; !found {
				byID[r.id] = r
			}
		}
	}

	c.mergePersistentFlags()
	flags := c.Flags()

	resolved := make([]resolvedConstraintRule, 0, len(rules))
	for _, r := range rules {
		budget := maxResolvedConstraintSize
		bound, err := bindConstraintExpr(r.expr, r.id, byID, flags, nil, &budget)
		if err != nil {
			return nil, err
		}
		rule := resolvedConstraintRule{id: r.id, expr: bound}
		seen := map[*flag.Flag]bool{}
		var collect func(e *boundConstraintExpr)
		collect = func(e *boundConstraintExpr) {
			if e.kind == constraintPresent && !seen[e.flag] {
				seen[e.flag] = true
				rule.flags = append(rule.flags, e.flag)
				rule.flagNames = append(rule.flagNames, e.flag.Name)
			}
			for _, child := range e.children {
				collect(child)
			}
		}
		collect(bound)
		resolved = append(resolved, rule)
	}
	return resolved, nil
}

// bindConstraintExpr expands references and binds Present leaves to flags.
func bindConstraintExpr(expr *ConstraintExpr, ruleID string, byID map[string]*flagConstraintRule, flags *flag.FlagSet, refChain []string, budget *int) (*boundConstraintExpr, error) {
	*budget--
	if *budget < 0 {
		return nil, fmt.Errorf("flag constraint %q exceeds the maximum resolved expression size of %d nodes", ruleID, maxResolvedConstraintSize)
	}
	bound := &boundConstraintExpr{kind: expr.kind}
	switch expr.kind {
	case constraintPresent:
		f := flags.Lookup(expr.flagName)
		if f == nil {
			return nil, fmt.Errorf("flag constraint %q references unknown flag %q", ruleID, expr.flagName)
		}
		bound.flag = f
	case constraintRef:
		for i, id := range refChain {
			if id == expr.refID {
				return nil, fmt.Errorf("flag constraint %q has a circular reference: %s", ruleID, strings.Join(append(refChain[i:], expr.refID), " -> "))
			}
		}
		target, found := byID[expr.refID]
		if !found {
			return nil, fmt.Errorf("flag constraint %q references unknown constraint rule %q", ruleID, expr.refID)
		}
		return bindConstraintExpr(target.expr, ruleID, byID, flags, append(refChain, expr.refID), budget)
	default:
		bound.children = make([]*boundConstraintExpr, len(expr.children))
		for i, child := range expr.children {
			var err error
			bound.children[i], err = bindConstraintExpr(child, ruleID, byID, flags, refChain, budget)
			if err != nil {
				return nil, err
			}
		}
	}
	return bound, nil
}

// ValidateFlagConstraints validates the flag constraint expressions
// registered on this command and its parents. Violations are reported in
// rule registration order, each with its rule ID and the bound flag names.
func (c *Command) ValidateFlagConstraints() error {
	if c.DisableFlagParsing {
		return nil
	}
	rules, err := c.resolveFlagConstraints()
	if err != nil {
		return err
	}
	present := func(f *flag.Flag) bool { return f.Changed }
	var violations []string
	for _, rule := range rules {
		if !rule.expr.eval(present) {
			violations = append(violations, fmt.Sprintf("%q (flags: %s): %s",
				rule.id, strings.Join(rule.flagNames, ", "), rule.expr.describe(present)))
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("flag constraint violated: %s", strings.Join(violations, "; "))
	}
	return nil
}

// flagConstraintChecker filters flag name completion candidates: a
// candidate is dropped only if marking it present would make it impossible
// to ever satisfy the registered constraints. Flags already provided are
// fixed as present; flags not yet provided remain free to be set later.
type flagConstraintChecker struct {
	rules    []resolvedConstraintRule
	vars     []*flag.Flag
	varIndex map[*flag.Flag]int
	enabled  bool
}

func newFlagConstraintChecker(c *Command) *flagConstraintChecker {
	checker := &flagConstraintChecker{}
	rules, err := c.resolveFlagConstraints()
	if err != nil || len(rules) == 0 {
		return checker
	}
	checker.varIndex = map[*flag.Flag]int{}
	for _, rule := range rules {
		for _, f := range rule.flags {
			if _, found := checker.varIndex[f]; !found {
				checker.varIndex[f] = len(checker.vars)
				checker.vars = append(checker.vars, f)
			}
		}
	}
	if len(checker.vars) > maxConstraintCompletionVars {
		// Too many variables for a bounded search; leave candidates untouched.
		return checker
	}
	checker.rules = rules
	checker.enabled = true
	return checker
}

// allows reports whether the given flag may still be offered as a
// completion candidate.
func (ch *flagConstraintChecker) allows(f *flag.Flag) bool {
	if !ch.enabled {
		return true
	}
	idx, found := ch.varIndex[f]
	if !found {
		return true
	}
	values := make([]bool, len(ch.vars))
	fixed := make([]bool, len(ch.vars))
	for i, v := range ch.vars {
		if v.Changed {
			values[i] = true
			fixed[i] = true
		}
	}
	values[idx] = true
	fixed[idx] = true
	return ch.satisfiable(values, fixed)
}

// satisfiable reports whether the rules can all be satisfied with the fixed
// variables kept as given and the free variables assigned either way.
func (ch *flagConstraintChecker) satisfiable(values, fixed []bool) bool {
	evalAll := func() bool {
		present := func(f *flag.Flag) bool { return values[ch.varIndex[f]] }
		for _, rule := range ch.rules {
			if !rule.expr.eval(present) {
				return false
			}
		}
		return true
	}
	var search func(i int) bool
	search = func(i int) bool {
		for ; i < len(values); i++ {
			if !fixed[i] {
				break
			}
		}
		if i == len(values) {
			return evalAll()
		}
		for _, b := range []bool{false, true} {
			values[i] = b
			if search(i + 1) {
				values[i] = false
				return true
			}
		}
		values[i] = false
		return false
	}
	return search(0)
}
