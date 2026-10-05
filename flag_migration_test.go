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
	"strings"
	"testing"
)

// TestFlagMigrationMapsOldToNew verifies that a deprecated flag name is
// migrated to its canonical name and the value is set on the canonical flag.
func TestFlagMigrationMapsOldToNew(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	output, err := executeCommand(c, "--old-flag=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if newValue != "value" {
		t.Errorf("Expected new-flag value %q, got %q", "value", newValue)
	}
	if !strings.Contains(output, "Flag --old-flag has been deprecated, use --new-flag instead") {
		t.Errorf("Expected deprecation warning, got: %q", output)
	}
}

// TestFlagMigrationMapsOldToNewWithoutEquals verifies that --old-flag value
// (space separated) is migrated correctly.
func TestFlagMigrationMapsOldToNewWithoutEquals(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag", "value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if newValue != "value" {
		t.Errorf("Expected new-flag value %q, got %q", "value", newValue)
	}
}

// TestFlagMigrationConflictRejectsBeforePreRun verifies that providing both
// the deprecated and canonical names results in an error and that PreRun is
// never executed.
func TestFlagMigrationConflictRejectsBeforePreRun(t *testing.T) {
	var preRunCount int
	var newValue string
	c := &Command{
		Use:    "c",
		Args:   NoArgs,
		PreRun: func(*Command, []string) { preRunCount++ },
		Run:    func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag=old", "--new-flag=new")
	if err == nil {
		t.Fatal("Expected error when both old and new flags are provided")
	}
	if preRunCount != 0 {
		t.Errorf("Expected PreRun to run 0 times, got %d", preRunCount)
	}
	if !strings.Contains(err.Error(), "cannot be used together") {
		t.Errorf("Expected conflict error message, got: %v", err)
	}
}

// TestFlagMigrationConflictRejectsBeforePreRunReversed verifies the conflict
// is detected regardless of the order the flags are provided.
func TestFlagMigrationConflictRejectsBeforePreRunReversed(t *testing.T) {
	var preRunCount int
	var newValue string
	c := &Command{
		Use:    "c",
		Args:   NoArgs,
		PreRun: func(*Command, []string) { preRunCount++ },
		Run:    func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--new-flag=new", "--old-flag=old")
	if err == nil {
		t.Fatal("Expected error when both new and old flags are provided")
	}
	if preRunCount != 0 {
		t.Errorf("Expected PreRun to run 0 times, got %d", preRunCount)
	}
}

// TestFlagMigrationChangedBelongsToCanonical verifies that when the deprecated
// flag is used, the canonical flag's Changed field is true.
func TestFlagMigrationChangedBelongsToCanonical(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !c.Flags().Changed("new-flag") {
		t.Error("Expected canonical flag new-flag to be marked as Changed")
	}
}

// TestFlagMigrationWarningOnce verifies the deprecation warning is printed
// exactly once even if the deprecated flag is provided multiple times.
func TestFlagMigrationWarningOnce(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	output, err := executeCommand(c, "--old-flag=a", "--old-flag=b")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if count := strings.Count(output, "Flag --old-flag has been deprecated"); count != 1 {
		t.Errorf("Expected warning once, got %d times: %q", count, output)
	}
	if newValue != "b" {
		t.Errorf("Expected new-flag value %q, got %q", "b", newValue)
	}
}

// TestFlagMigrationMultipleOldsToSameNew verifies that multiple deprecated
// names can map to the same canonical flag.
func TestFlagMigrationMultipleOldsToSameNew(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(
		FlagMigration{Old: "old-one", New: "new-flag"},
		FlagMigration{Old: "old-two", New: "new-flag"},
	)

	_, err := executeCommand(c, "--old-one=a", "--old-two=b")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if newValue != "b" {
		t.Errorf("Expected new-flag value %q, got %q", "b", newValue)
	}
}

// TestFlagMigrationSubcommandLocalFlagShadowsParent verifies that when a
// subcommand has a local flag with the same name as a parent persistent flag,
// the migration targets the subcommand's local flag rather than the parent's.
func TestFlagMigrationSubcommandLocalFlagShadowsParent(t *testing.T) {
	var parentValue string
	var childValue string
	parent := &Command{Use: "parent", Run: func(*Command, []string) {}}
	parent.PersistentFlags().StringVar(&parentValue, "shared", "", "")

	child := &Command{
		Use:  "child",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	child.Flags().StringVar(&childValue, "shared", "", "")
	child.SetFlagMigrations(FlagMigration{Old: "old-shared", New: "shared"})

	parent.AddCommand(child)

	_, err := executeCommand(parent, "child", "--old-shared=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if childValue != "value" {
		t.Errorf("Expected child local flag value %q, got %q", "value", childValue)
	}
	if parentValue != "" {
		t.Errorf("Expected parent persistent flag to remain unchanged, got %q", parentValue)
	}
	if !child.Flags().Changed("shared") {
		t.Error("Expected child local flag shared to be marked as Changed")
	}
}

// TestFlagMigrationParentPersistentFlag verifies that a deprecated flag is
// migrated to a parent persistent flag when the subcommand does not shadow it.
func TestFlagMigrationParentPersistentFlag(t *testing.T) {
	var parentValue string
	parent := &Command{Use: "parent", Run: func(*Command, []string) {}}
	parent.PersistentFlags().StringVar(&parentValue, "new-flag", "", "")

	child := &Command{
		Use:  "child",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	parent.AddCommand(child)
	parent.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(parent, "child", "--old-flag=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if parentValue != "value" {
		t.Errorf("Expected parent persistent flag value %q, got %q", "value", parentValue)
	}
}

// TestFlagMigrationUnknownOldFlagUnchanged verifies that a deprecated flag
// name that is not in the migration table is left unchanged (and therefore
// reported as an unknown flag).
func TestFlagMigrationUnknownOldFlagUnchanged(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--unknown-flag=value")
	if err == nil {
		t.Fatal("Expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("Expected unknown flag error, got: %v", err)
	}
}

// TestFlagMigrationOldFlagStillRegistered verifies that when the deprecated
// name is still registered as a real flag, it is not intercepted by the
// migration table.
func TestFlagMigrationOldFlagStillRegistered(t *testing.T) {
	var oldValue string
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&oldValue, "old-flag", "", "")
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag=old")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if oldValue != "old" {
		t.Errorf("Expected old-flag value %q, got %q", "old", oldValue)
	}
	if newValue != "" {
		t.Errorf("Expected new-flag to remain unchanged, got %q", newValue)
	}
}

// TestFlagMigrationNoMigrations verifies that a command without a migration
// table behaves exactly as before.
func TestFlagMigrationNoMigrations(t *testing.T) {
	var value string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&value, "flag", "", "")

	_, err := executeCommand(c, "--flag=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if value != "value" {
		t.Errorf("Expected flag value %q, got %q", "value", value)
	}
}

// TestFlagMigrationInherited verifies that a subcommand inherits the migration
// table from its parent.
func TestFlagMigrationInherited(t *testing.T) {
	var newValue string
	parent := &Command{Use: "parent", Run: func(*Command, []string) {}}
	parent.PersistentFlags().StringVar(&newValue, "new-flag", "", "")
	parent.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	child := &Command{
		Use:  "child",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	parent.AddCommand(child)

	_, err := executeCommand(parent, "child", "--old-flag=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if newValue != "value" {
		t.Errorf("Expected new-flag value %q, got %q", "value", newValue)
	}
}

// TestFlagMigrationCleared verifies that a subcommand can clear an inherited
// migration table by setting an empty one.
func TestFlagMigrationCleared(t *testing.T) {
	var newValue string
	parent := &Command{Use: "parent", Run: func(*Command, []string) {}}
	parent.PersistentFlags().StringVar(&newValue, "new-flag", "", "")
	parent.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	child := &Command{
		Use:  "child",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	child.SetFlagMigrations()
	parent.AddCommand(child)

	_, err := executeCommand(parent, "child", "--old-flag=value")
	if err == nil {
		t.Fatal("Expected error when migration table is cleared")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("Expected unknown flag error, got: %v", err)
	}
}

// TestFlagMigrationBooleanFlag verifies that a deprecated boolean flag is
// migrated correctly without consuming the following argument.
func TestFlagMigrationBooleanFlag(t *testing.T) {
	var newValue bool
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().BoolVar(&newValue, "new-flag", false, "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !newValue {
		t.Error("Expected new-flag to be true")
	}
}

// TestFlagMigrationConflictWithShorthand verifies that providing the
// deprecated name and a shorthand for the canonical name is also rejected.
func TestFlagMigrationConflictWithShorthand(t *testing.T) {
	var preRunCount int
	var newValue string
	c := &Command{
		Use:    "c",
		Args:   NoArgs,
		PreRun: func(*Command, []string) { preRunCount++ },
		Run:    func(*Command, []string) {},
	}
	c.Flags().StringVarP(&newValue, "new-flag", "n", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag=old", "-nnew")
	if err == nil {
		t.Fatal("Expected error when both old and new flags are provided")
	}
	if preRunCount != 0 {
		t.Errorf("Expected PreRun to run 0 times, got %d", preRunCount)
	}
	if !strings.Contains(err.Error(), "cannot be used together") {
		t.Errorf("Expected conflict error message, got: %v", err)
	}
}

// TestFlagMigrationSliceFlag verifies that a deprecated slice flag is migrated
// correctly, accumulating values on the canonical slice flag.
func TestFlagMigrationSliceFlag(t *testing.T) {
	var newValue []string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringSliceVar(&newValue, "new-flag", []string{}, "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	_, err := executeCommand(c, "--old-flag=a", "--old-flag=b", "--old-flag=c")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(newValue) != 3 || newValue[0] != "a" || newValue[1] != "b" || newValue[2] != "c" {
		t.Errorf("Expected new-flag values [a b c], got %v", newValue)
	}
}

// TestFlagMigrationDoesNotModifyRegistration verifies that applying a migration
// does not add the deprecated name to the command's flag set.
func TestFlagMigrationDoesNotModifyRegistration(t *testing.T) {
	var newValue string
	c := &Command{
		Use:  "c",
		Args: NoArgs,
		Run:  func(*Command, []string) {},
	}
	c.Flags().StringVar(&newValue, "new-flag", "", "")
	c.SetFlagMigrations(FlagMigration{Old: "old-flag", New: "new-flag"})

	if c.Flags().Lookup("old-flag") != nil {
		t.Error("Expected old-flag to not be registered before execution")
	}

	_, err := executeCommand(c, "--old-flag=value")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if c.Flags().Lookup("old-flag") != nil {
		t.Error("Expected old-flag to not be registered after execution")
	}
}
