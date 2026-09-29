package cobra

import (
	"bytes"
	"strings"
	"testing"
)

func migrationTestRoot() (*Command, *[]string, *string) {
	var gotArgs []string
	var gotMessage string

	projectCmd := &Command{Use: "project"}
	pullCmd := &Command{
		Use: "pull",
		Run: func(cmd *Command, args []string) {
			gotArgs = append([]string{}, args...)
			gotMessage, _ = cmd.Flags().GetString("message")
		},
	}
	pullCmd.Flags().String("message", "", "message")
	projectCmd.AddCommand(pullCmd)

	rootCmd := &Command{Use: "root"}
	rootCmd.AddCommand(projectCmd)
	return rootCmd, &gotArgs, &gotMessage
}

func executeMigration(rootCmd *Command, args ...string) (stdout, stderr string, err error) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return out.String(), errOut.String(), err
}

func migrationNoticeCount(s string) int {
	return strings.Count(s, "is deprecated, please use")
}

func TestAliasMigrationRewritesPathAndPreservesArgs(t *testing.T) {
	rootCmd, gotArgs, gotMessage := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{
		"old sync": "project pull",
	})

	stdout, stderr, err := executeMigration(rootCmd,
		"old", "sync", "--message", "hello world", "positional", "--", "--after-dash", "still here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *gotMessage != "hello world" {
		t.Errorf("flag value = %q, want %q", *gotMessage, "hello world")
	}
	wantArgs := []string{"positional", "--after-dash", "still here"}
	if len(*gotArgs) != len(wantArgs) {
		t.Fatalf("target args = %q, want %q", *gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if (*gotArgs)[i] != wantArgs[i] {
			t.Fatalf("target args = %q, want %q", *gotArgs, wantArgs)
		}
	}

	if migrationNoticeCount(stdout) != 1 {
		t.Errorf("expected exactly one migration notice, got %q", stdout)
	}
	if !strings.Contains(stdout, `"old sync"`) || !strings.Contains(stdout, `"project pull"`) {
		t.Errorf("notice must mention old and new path, got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected no stderr, got %q", stderr)
	}
}

func TestAliasMigrationRealSubcommandWinsCollision(t *testing.T) {
	var ran string
	rootCmd, _, _ := migrationTestRoot()

	// A real subcommand whose name collides with the first token of a mapping.
	rootCmd.AddCommand(&Command{
		Use: "old",
		Run: func(cmd *Command, args []string) {
			ran = "real"
		},
	})
	rootCmd.SetAliasMigration(map[string]string{
		"old sync": "project pull",
	})

	stdout, _, err := executeMigration(rootCmd, "old", "sync")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran != "real" {
		t.Fatalf("expected the real subcommand to run, got %q", ran)
	}
	if migrationNoticeCount(stdout) != 0 {
		t.Errorf("real subcommand must not trigger a migration notice: %q", stdout)
	}
}

func TestAliasMigrationChainedMappingsPrintNoticeOnce(t *testing.T) {
	rootCmd, gotArgs, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{
		"legacy fetch": "old sync",
		"old sync":     "project pull",
	})

	stdout, stderr, err := executeMigration(rootCmd, "legacy", "fetch", "keep-me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*gotArgs) != 1 || (*gotArgs)[0] != "keep-me" {
		t.Fatalf("target args = %q, want [keep-me]", *gotArgs)
	}
	if c := migrationNoticeCount(stdout); c != 1 {
		t.Errorf("expected exactly one notice across the chain, got %d in %q", c, stdout)
	}
	if !strings.Contains(stdout, `"legacy fetch"`) || !strings.Contains(stdout, `"project pull"`) {
		t.Errorf("notice must mention original and final path, got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected no stderr, got %q", stderr)
	}
}

func TestAliasMigrationCycle(t *testing.T) {
	rootCmd, _, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{
		"old sync":     "project pull",
		"project pull": "legacy fetch",
		"legacy fetch": "old sync",
	})

	stdout, stderr, err := executeMigration(rootCmd, "old", "sync")
	if err == nil {
		t.Fatal("expected an error for a migration cycle")
	}
	if migrationNoticeCount(stdout) != 0 {
		t.Errorf("cycle must not print a migration notice: %q", stdout)
	}
	if c := strings.Count(stderr, "cycle"); c != 1 {
		t.Errorf("expected one cycle error, got %q", stderr)
	}
}

func TestAliasMigrationBadTarget(t *testing.T) {
	rootCmd, _, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{
		"old sync": "project nope",
	})

	stdout, stderr, err := executeMigration(rootCmd, "old", "sync", "--message", "hello world")
	if err == nil {
		t.Fatal("expected an error when the target command does not exist")
	}
	// A bad target is a configuration error: it must fail before any
	// migration notice is shown.
	if migrationNoticeCount(stdout) != 0 {
		t.Errorf("bad target must not print a migration notice: %q", stdout)
	}
	if c := strings.Count(stderr, "unknown command"); c != 1 {
		t.Errorf("expected one unknown command error, got %q", stderr)
	}
}

func TestAliasMigrationChainedBadTarget(t *testing.T) {
	rootCmd, _, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{
		"legacy fetch": "old sync",
		"old sync":     "project ghost",
	})

	stdout, stderr, err := executeMigration(rootCmd, "legacy", "fetch")
	if err == nil {
		t.Fatal("expected an error when the final target does not exist")
	}
	if migrationNoticeCount(stdout) != 0 {
		t.Errorf("must not show a notice when the final target is invalid: %q", stdout)
	}
	if c := strings.Count(stderr, "unknown command"); c != 1 {
		t.Errorf("expected one unknown command error, got %q", stderr)
	}
}

func TestAliasMigrationHelpFlagShowsTargetHelpOnce(t *testing.T) {
	rootCmd, _, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{
		"old sync": "project pull",
	})

	stdout, stderr, err := executeMigration(rootCmd, "old", "sync", "--help", "--", "x")
	if err != nil {
		t.Fatalf("--help must not return an error, got %v", err)
	}
	if c := migrationNoticeCount(stdout); c != 1 {
		t.Errorf("expected one migration notice, got %d in %q", c, stdout)
	}
	if c := strings.Count(stdout, "project pull"); c < 2 {
		t.Errorf("expected help output for the target command, got %q", stdout)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("expected target command help, got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected no stderr, got %q", stderr)
	}
}

func TestAliasMigrationGetter(t *testing.T) {
	rootCmd, _, _ := migrationTestRoot()
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	if got, ok := rootCmd.AliasMigration("old sync"); !ok || got != "project pull" {
		t.Errorf("AliasMigration = %q, %v, want project pull, true", got, ok)
	}
	if _, ok := rootCmd.AliasMigration("missing"); ok {
		t.Errorf("unexpected migration found for missing path")
	}
}

func TestAliasMigrationTargetReceivesRemainingArgsVerbatim(t *testing.T) {
	var gotArgs []string

	rootCmd := &Command{Use: "root"}
	targetParent := &Command{Use: "project"}
	target := &Command{
		Use:                "pull",
		DisableFlagParsing: true,
		Run: func(cmd *Command, args []string) {
			gotArgs = append([]string{}, args...)
		},
	}
	targetParent.AddCommand(target)
	rootCmd.AddCommand(targetParent)
	rootCmd.SetAliasMigration(map[string]string{"old sync": "project pull"})

	input := []string{"old", "sync", "--message", "hello world", "positional", "--", "--after-dash", "still here"}
	_, stderr, err := executeMigration(rootCmd, input...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}

	want := input[2:]
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("target received %q, want %q verbatim", gotArgs, want)
	}
}
