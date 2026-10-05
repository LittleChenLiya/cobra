package cobra

import (
	"bytes"
	"strings"
	"testing"

	flag "github.com/spf13/pflag"
)

func TestFlagMigrationValueLandsOnCanonical(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	if _, err := executeCommand(rootCmd, "--old", "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := rootCmd.Flags().GetString("new")
	if err != nil {
		t.Fatalf("canonical flag lookup error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected --new to receive the value from --old, got %q", got)
	}
	if !rootCmd.Flags().Changed("new") {
		t.Fatal("expected canonical flag --new to be reported as Changed")
	}
	if rootCmd.Flags().Changed("old") {
		t.Fatal("the deprecated name must never be registered as a changed flag")
	}
}

func TestFlagMigrationEqualsForm(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	if _, err := executeCommand(rootCmd, "--old=world"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := rootCmd.Flags().GetString("new")
	if got != "world" {
		t.Fatalf("expected %q, got %q", "world", got)
	}
}

func TestFlagMigrationNoRegistrationMeansNoBehaviorChange(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")

	_, err := executeCommand(rootCmd, "--old", "x")
	if err == nil {
		t.Fatal("expected unknown-flag error when no migration is registered")
	}
}

func TestFlagMigrationConflictBeforePreRun(t *testing.T) {
	preRunCount := 0
	rootCmd := &Command{
		Use: "root",
		PreRun: func(_ *Command, _ []string) {
			preRunCount++
		},
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	_, err := executeCommand(rootCmd, "--old", "a", "--new", "b")
	if err == nil {
		t.Fatal("expected a conflict error when --old and --new are both given")
	}
	if preRunCount != 0 {
		t.Fatalf("expected PreRun to run 0 times, ran %d times", preRunCount)
	}
	if rootCmd.Flags().Changed("new") {
		t.Fatal("canonical flag must not be considered changed after a conflict")
	}
}

func TestFlagMigrationConflictBothOrders(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	if _, err := executeCommand(rootCmd, "--new", "b", "--old", "a"); err == nil {
		t.Fatal("expected a conflict error regardless of argument order")
	}
}

func TestTwoDeprecatedAliasesForSameCanonicalConflict(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old-a": "new", "old-b": "new"})

	if _, err := executeCommand(rootCmd, "--old-a", "a", "--old-b", "b"); err == nil {
		t.Fatal("expected a conflict error when two aliases are both given")
	}

	rootCmd2 := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd2.Flags().String("new", "default", "canonical flag")
	rootCmd2.SetFlagMigrations(map[string]string{"old-a": "new", "old-b": "new"})
	if _, err := executeCommand(rootCmd2, "--old-b", "z"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := rootCmd2.Flags().GetString("new")
	if got != "z" {
		t.Fatalf("expected %q, got %q", "z", got)
	}
}

func TestFlagMigrationWarningAppearsOnce(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--trace", "--trace"})
	rootCmd.Flags().Bool("verbose", false, "canonical bool flag")
	rootCmd.SetFlagMigrations(map[string]string{"trace": "verbose"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c := strings.Count(buf.String(), "Flag --trace has been deprecated"); c != 1 {
		t.Fatalf("expected exactly one migration warning, got %d in %q", c, buf.String())
	}
	if !rootCmd.Flags().Changed("verbose") {
		t.Fatal("expected canonical bool flag to be Changed")
	}
}

func TestChildLocalFlagShadowsParentMigration(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	rootCmd.PersistentFlags().String("new", "default", "parent persistent canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	childCmd := &Command{
		Use: "child",
		Run: func(_ *Command, _ []string) {},
	}
	// A child local flag named "old" shadows the deprecated name, so the parent
	// migration must not fire on the child.
	childCmd.Flags().String("old", "child-default", "child local flag")
	rootCmd.AddCommand(childCmd)

	if _, err := executeCommand(rootCmd, "child", "--old", "child-value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := childCmd.Flags().GetString("old")
	if got != "child-value" {
		t.Fatalf("expected child local --old to keep its value, got %q", got)
	}
	newVal, _ := childCmd.Flags().GetString("new")
	if newVal != "default" {
		t.Fatalf("parent canonical --new must stay at default, got %q", newVal)
	}
	if childCmd.Flags().Changed("new") {
		t.Fatal("the migration must not redirect a shadowed local flag to the parent")
	}
	if !childCmd.Flags().Changed("old") {
		t.Fatal("the child local --old should be reported as Changed")
	}
}

func TestParentPersistentMigrationWorksOnChild(t *testing.T) {
	rootCmd := &Command{Use: "root"}
	rootCmd.PersistentFlags().String("new", "default", "parent persistent canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	childCmd := &Command{
		Use: "child",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.AddCommand(childCmd)

	if _, err := executeCommand(rootCmd, "child", "--old", "inherited"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := childCmd.Flags().GetString("new")
	if got != "inherited" {
		t.Fatalf("expected inherited canonical flag to receive %q, got %q", "inherited", got)
	}
	if !childCmd.Flags().Changed("new") {
		t.Fatal("expected inherited canonical flag to be reported as Changed")
	}
}

func TestAddFlagMigrationAndGetter(t *testing.T) {
	c := &Command{Use: "c"}
	if c.FlagMigrations() != nil {
		t.Fatal("expected nil migrations before registration")
	}
	c.AddFlagMigration("old", "new")
	got := c.FlagMigrations()
	if got["old"] != "new" {
		t.Fatalf("unexpected migration table: %v", got)
	}
	got["old"] = "mutated"
	if c.FlagMigrations()["old"] != "new" {
		t.Fatal("FlagMigrations must return a defensive copy")
	}

	c.SetFlagMigrations(nil)
	if c.FlagMigrations() != nil {
		t.Fatal("expected nil migrations after reset")
	}
}

func TestFlagMigrationShortFormCanonical(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().StringP("new", "n", "default", "canonical flag with shorthand")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	if _, err := executeCommand(rootCmd, "-n", "short"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := rootCmd.Flags().GetString("new")
	if got != "short" {
		t.Fatalf("expected %q, got %q", "short", got)
	}

	if _, err := executeCommand(rootCmd, "-n", "short", "--old", "long"); err == nil {
		t.Fatal("expected conflict between shorthand canonical and deprecated name")
	}
}

func TestFlagMigrationBoolDoesNotConsumeNextArg(t *testing.T) {
	rootCmd := &Command{
		Use:  "root",
		Args: ArbitraryArgs,
		Run:  func(_ *Command, _ []string) {},
	}
	var gotArgs []string
	rootCmd.Run = func(_ *Command, args []string) {
		gotArgs = args
	}
	rootCmd.Flags().Bool("verbose", false, "canonical bool flag")
	rootCmd.SetFlagMigrations(map[string]string{"trace": "verbose"})

	if _, err := executeCommand(rootCmd, "--trace", "positional"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, _ := rootCmd.Flags().GetBool("verbose")
	if !v {
		t.Fatal("expected canonical bool flag to be true")
	}
	if len(gotArgs) != 1 || gotArgs[0] != "positional" {
		t.Fatalf("expected positional argument to be preserved, got %v", gotArgs)
	}
}

func TestFlagMigrationValueBeforePositional(t *testing.T) {
	rootCmd := &Command{
		Use:  "root",
		Args: ArbitraryArgs,
		Run:  func(_ *Command, _ []string) {},
	}
	var gotArgs []string
	rootCmd.Run = func(_ *Command, args []string) {
		gotArgs = args
	}
	rootCmd.Flags().String("name", "default", "canonical string flag")
	rootCmd.SetFlagMigrations(map[string]string{"username": "name"})

	if _, err := executeCommand(rootCmd, "--username", "alice", "file.txt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, _ := rootCmd.Flags().GetString("name")
	if v != "alice" {
		t.Fatalf("expected --name=alice, got %q", v)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "file.txt" {
		t.Fatalf("expected the value to be consumed and file.txt positional, got %v", gotArgs)
	}
}

func TestFlagMigrationAfterDoubleDashIsNotRewritten(t *testing.T) {
	rootCmd := &Command{
		Use:  "root",
		Args: ArbitraryArgs,
		Run:  func(_ *Command, _ []string) {},
	}
	var gotArgs []string
	rootCmd.Run = func(_ *Command, args []string) {
		gotArgs = args
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	if _, err := executeCommand(rootCmd, "--", "--old", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rootCmd.Flags().Changed("new") {
		t.Fatal("flags after -- must not be rewritten")
	}
	if len(gotArgs) != 2 || gotArgs[0] != "--old" || gotArgs[1] != "x" {
		t.Fatalf("expected raw args after --, got %v", gotArgs)
	}
}

func TestFlagMigrationConflictErrorMessage(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old": "new"})

	_, err := executeCommand(rootCmd, "--old", "a", "--new", "b")
	if err == nil || !strings.Contains(err.Error(), "cannot be specified together") {
		t.Fatalf("expected a descriptive conflict error, got %v", err)
	}
}

func TestFlagMigrationWithGlobalNormalization(t *testing.T) {
	rootCmd := &Command{
		Use: "root",
		Run: func(_ *Command, _ []string) {},
	}
	rootCmd.Flags().String("new-name", "default", "canonical flag")
	rootCmd.SetFlagMigrations(map[string]string{"old-name": "new-name"})
	// Underscores on the command line normalize to dashes, like pflag does.
	rootCmd.SetGlobalNormalizationFunc(func(_ *flag.FlagSet, name string) flag.NormalizedName {
		return flag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})

	if _, err := executeCommand(rootCmd, "--old_name", "norm"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, _ := rootCmd.Flags().GetString("new-name")
	if v != "norm" {
		t.Fatalf("expected normalized migration to land on --new-name, got %q", v)
	}
}

func TestFlagMigrationTraverseChildrenCrossLevel(t *testing.T) {
	// Deprecated name at the root level, canonical name at the child level:
	// still a conflict even though each level parses separately.
	root := &Command{Use: "root", TraverseChildren: true}
	root.PersistentFlags().String("new", "default", "canonical")
	root.SetFlagMigrations(map[string]string{"old": "new"})
	child := &Command{Use: "child", Run: func(_ *Command, _ []string) {}}
	root.AddCommand(child)

	if _, err := executeCommand(root, "--old", "a", "child", "--new", "b"); err == nil {
		t.Fatal("expected cross-level conflict under TraverseChildren")
	}

	// Consistent source across levels is allowed (last value wins as usual).
	root2 := &Command{Use: "root", TraverseChildren: true}
	root2.PersistentFlags().String("new", "default", "canonical")
	root2.SetFlagMigrations(map[string]string{"old": "new"})
	child2 := &Command{Use: "child", Run: func(_ *Command, _ []string) {}}
	root2.AddCommand(child2)
	if _, err := executeCommand(root2, "--old", "a", "child", "--old", "b"); err != nil {
		t.Fatalf("unexpected error for repeated alias: %v", err)
	}
	if v, _ := child2.Flags().GetString("new"); v != "b" {
		t.Fatalf("expected last value b, got %q", v)
	}
}
