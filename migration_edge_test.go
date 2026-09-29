package cobra

import (
	"bytes"
	"strings"
	"testing"
)

func TestAliasMigrationWithTraverseChildren(t *testing.T) {
	var gotArgs []string

	rootCmd := &Command{Use: "root", TraverseChildren: true}
	rootCmd.PersistentFlags().Bool("verbose", false, "verbose")
	parent := &Command{Use: "project"}
	target := &Command{
		Use: "pull",
		Run: func(cmd *Command, args []string) {
			gotArgs = append([]string{}, args...)
		},
	}
	parent.AddCommand(target)
	rootCmd.AddCommand(parent)
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	_, stderr, err := executeMigration(rootCmd, "--verbose", "old", "sync", "file name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "file name" {
		t.Fatalf("args = %q, want [file name]", gotArgs)
	}
}

func TestAliasMigrationCommandAliasCollision(t *testing.T) {
	var ran string

	rootCmd := &Command{Use: "root"}
	project := &Command{Use: "project"}
	pull := &Command{
		Use:     "pull",
		Aliases: []string{"sync"},
		Run: func(cmd *Command, args []string) {
			ran = "pull"
		},
	}
	project.AddCommand(pull)
	rootCmd.AddCommand(project)

	// Even though the mapping exists, the real alias must win and no notice
	// may be printed.
	rootCmd.SetAliasMigration(map[string]string{"project sync": "project pull"})

	out := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetArgs([]string{"project", "sync"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran != "pull" {
		t.Fatalf("expected pull command to run via its real alias, got %q", ran)
	}
	if strings.Contains(out.String(), "is deprecated, please use") {
		t.Errorf("real command alias must not trigger migration notice: %q", out.String())
	}
}

func TestAliasMigrationNoMatchLeavesArgsUntouched(t *testing.T) {
	rootCmd, gotArgs, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	_, _, err := executeMigration(rootCmd, "project", "pull", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*gotArgs) != 1 || (*gotArgs)[0] != "x" {
		t.Fatalf("args = %q, want [x]", *gotArgs)
	}
}

func TestAliasMigrationFlagsBeforeOldPathKeepValues(t *testing.T) {
	var gotArgs []string

	rootCmd := &Command{Use: "root"}
	project := &Command{Use: "project"}
	pull := &Command{
		Use:                "pull",
		DisableFlagParsing: true,
		Run: func(cmd *Command, args []string) {
			gotArgs = append([]string{}, args...)
		},
	}
	project.AddCommand(pull)
	rootCmd.AddCommand(project)
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose")
	rootCmd.PersistentFlags().String("root-flag", "", "root flag")
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	input := []string{"--root-flag", "root-value", "old", "-v", "sync", "tail"}
	_, _, err := executeMigration(rootCmd, input...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--root-flag", "root-value", "-v", "tail"}
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("target args = %q, want %q", gotArgs, want)
	}
}

func TestAliasMigrationPartialPrefixDoesNotMatch(t *testing.T) {
	rootCmd, gotArgs, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	// "old" alone is not a mapping; it must remain an unknown command.
	_, stderr, err := executeMigration(rootCmd, "old")
	if err == nil {
		t.Fatal("expected an unknown command error")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("expected unknown command error, got %q", stderr)
	}
	if gotArgs != nil && len(*gotArgs) != 0 {
		t.Errorf("target must not run, got %q", *gotArgs)
	}
}

func TestAliasMigrationCompletionUnaffected(t *testing.T) {
	rootCmd, _, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	_, _, err := executeMigration(rootCmd, "__complete", "")
	if err != nil {
		t.Fatalf("completion request must not be rewritten: %v", err)
	}
}
