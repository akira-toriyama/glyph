package parser

import "testing"

func TestCleanup(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "leading blank line does not become the subject",
			// git's editor buffer starts empty; a developer who types the
			// subject on line 2 used to be told the message was empty.
			in:   "\n:sparkles:(hook) add the thing\n",
			want: ":sparkles:(hook) add the thing",
		},
		{
			name: "editor template is stripped",
			in: ":bug: fix it\n" +
				"\n" +
				"# Please enter the commit message for your changes. Lines starting\n" +
				"# with '#' will be ignored, and an empty message aborts the commit.\n" +
				"#\n" +
				"# On branch main\n",
			want: ":bug: fix it",
		},
		{
			name: "template above the message does not become the subject",
			in: "# Please enter the commit message for your changes.\n" +
				"\n" +
				":memo: write the docs\n",
			want: ":memo: write the docs",
		},
		{
			name: "verbose diff below the scissors line is cut, not linted",
			in: ":zap: speed it up\n" +
				"\n" +
				"# ------------------------ >8 ------------------------\n" +
				"# Do not modify or remove the line above.\n" +
				"diff --git a/x b/x\n" +
				"+BREAKING CHANGE: this line lives in the diff, not the message\n",
			want: ":zap: speed it up",
		},
		{
			name: "indented scissors still cuts",
			in:   ":zap: go\n\n  #  ------ >8 ------  \ndiff --git a/x b/x\n",
			want: ":zap: go",
		},
		{
			name: "a body is preserved verbatim",
			in:   ":sparkles: add\n\nwhy this matters\n\nBREAKING CHANGE: it moved\n",
			want: ":sparkles: add\n\nwhy this matters\n\nBREAKING CHANGE: it moved",
		},
		{
			name: "comments-only reduces to empty",
			in:   "# Please enter the commit message\n#\n",
			want: "",
		},
		{
			name: "already-clean message is untouched",
			in:   ":bug: fix\n",
			want: ":bug: fix",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Cleanup(tt.in); got != tt.want {
				t.Errorf("Cleanup(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// A --range walk must NOT be cleaned: git log %B is already clean, and running
// Cleanup there would swallow a genuinely empty message and eat body lines a
// project chose to start with '#'. This test documents the boundary by pinning
// what Cleanup would destroy if it were ever applied there.
func TestCleanupIsNotSafeForAlreadyCleanedMessages(t *testing.T) {
	if got := Cleanup("#42 was the culprit\n"); got != "" {
		t.Errorf("expected a '#'-leading line to be treated as a comment, got %q", got)
	}
}
