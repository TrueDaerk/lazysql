package pathcomplete

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree builds a fixture directory: Development/, Downloads/, design.txt,
// notes.txt, .hidden/.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"Development", "Downloads", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"design.txt", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func sep() string { return string(filepath.Separator) }

func TestCompleteExtendsToCommonPrefix(t *testing.T) {
	root := tree(t)
	r := Complete(filepath.Join(root, "D"))
	// Development / Downloads share only "D": the input is already the
	// common prefix, so Completed stays put and both candidates are offered.
	if got, want := r.Completed, filepath.Join(root, "D"); got != want {
		t.Fatalf("Completed = %q, want %q (ambiguous D)", got, want)
	}
	if len(r.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want Development/ and Downloads/", r.Candidates)
	}
	for _, c := range r.Candidates {
		if !strings.HasSuffix(c, sep()) {
			t.Fatalf("directory candidate %q lacks trailing separator", c)
		}
	}
}

func TestCompleteSingleDirGetsSeparator(t *testing.T) {
	root := tree(t)
	r := Complete(filepath.Join(root, "Dev"))
	if want := filepath.Join(root, "Development") + sep(); r.Completed != want {
		t.Fatalf("Completed = %q, want %q", r.Completed, want)
	}
	if len(r.Candidates) != 1 {
		t.Fatalf("Candidates = %v", r.Candidates)
	}
}

func TestCompleteCaseInsensitiveFallback(t *testing.T) {
	root := tree(t)
	r := Complete(filepath.Join(root, "dev"))
	if want := filepath.Join(root, "Development") + sep(); r.Completed != want {
		t.Fatalf("Completed = %q, want %q", r.Completed, want)
	}
}

func TestCompleteExactCaseBeatsFolded(t *testing.T) {
	root := tree(t)
	// "de" matches design.txt exactly; Development only case-folded.
	r := Complete(filepath.Join(root, "de"))
	if len(r.Candidates) != 1 || !strings.HasSuffix(r.Candidates[0], "design.txt") {
		t.Fatalf("Candidates = %v, want design.txt only", r.Candidates)
	}
}

func TestCompleteHiddenNeedsExplicitDot(t *testing.T) {
	root := tree(t)
	if r := Complete(root + sep()); len(r.Candidates) != 4 {
		t.Fatalf("Candidates = %v, hidden entry should be excluded", r.Candidates)
	}
	r := Complete(root + sep() + ".")
	if len(r.Candidates) != 1 || !strings.HasSuffix(r.Candidates[0], ".hidden"+sep()) {
		t.Fatalf("Candidates = %v, want .hidden/", r.Candidates)
	}
}

func TestDirsOnly(t *testing.T) {
	root := tree(t)
	r := Dirs(root + sep())
	if len(r.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want the two directories", r.Candidates)
	}
	for _, c := range r.Candidates {
		if !strings.HasSuffix(c, sep()) {
			t.Fatalf("candidate %q lacks trailing separator", c)
		}
	}
}

func TestCompleteNoMatchKeepsInput(t *testing.T) {
	root := tree(t)
	in := filepath.Join(root, "zzz")
	r := Complete(in)
	if r.Completed != in || r.Candidates != nil {
		t.Fatalf("no-match should keep input: %+v", r)
	}
}

func TestCompleteEmptyAndBadDir(t *testing.T) {
	if r := Complete(""); r.Completed != "" || r.Candidates != nil {
		t.Fatalf("empty input: %+v", r)
	}
	in := filepath.Join(string(filepath.Separator), "no-such-dir-ike-test", "x")
	if r := Complete(in); r.Completed != in || r.Candidates != nil {
		t.Fatalf("unreadable dir should keep input: %+v", r)
	}
}

func TestCompletePreservesTildeNotation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Skip("home unreadable")
	}
	var name string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			name = e.Name()
			break
		}
	}
	if name == "" {
		t.Skip("home has no visible entries")
	}
	r := Complete("~" + sep() + name[:1])
	if len(r.Candidates) == 0 {
		t.Fatalf("expected candidates under ~%s for prefix %q", sep(), name[:1])
	}
	for _, c := range r.Candidates {
		if !strings.HasPrefix(c, "~"+sep()) {
			t.Fatalf("candidate %q lost the ~ notation", c)
		}
	}
	if !strings.HasPrefix(r.Completed, "~"+sep()) {
		t.Fatalf("Completed %q lost the ~ notation", r.Completed)
	}
}

func TestExpand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	if got := Expand("~"); got != home {
		t.Fatalf("Expand(~) = %q, want %q", got, home)
	}
	if got, want := Expand("~/x"), filepath.Join(home, "x"); got != want {
		t.Fatalf("Expand(~/x) = %q, want %q", got, want)
	}
	if got := Expand("~user/x"); got != "~user/x" {
		t.Fatalf("Expand(~user/x) = %q, want unchanged", got)
	}
	if got := Expand("/abs"); got != "/abs" {
		t.Fatalf("Expand(/abs) = %q, want unchanged", got)
	}
}

func TestExpandEnvVar(t *testing.T) {
	t.Setenv("LAZYSQL_TEST_DIR", "/tmp/lazysql-test")
	if got, want := Expand("$LAZYSQL_TEST_DIR/x"), "/tmp/lazysql-test/x"; got != want {
		t.Fatalf("Expand($VAR/x) = %q, want %q", got, want)
	}
	if got, want := Expand("${LAZYSQL_TEST_DIR}/x"), "/tmp/lazysql-test/x"; got != want {
		t.Fatalf("Expand(${VAR}/x) = %q, want %q", got, want)
	}
	if got, want := Expand("$LAZYSQL_TEST_DIR_UNSET/x"), "/x"; got != want {
		t.Fatalf("Expand(undefined $VAR) = %q, want %q", got, want)
	}
	if got := Expand("plain/path"); got != "plain/path" {
		t.Fatalf("Expand(no $) = %q, want unchanged", got)
	}
}

func TestCompleteExpandsEnvVar(t *testing.T) {
	root := tree(t)
	t.Setenv("LAZYSQL_TEST_ROOT", root)
	r := Complete("$LAZYSQL_TEST_ROOT" + sep() + "Dev")
	want := "$LAZYSQL_TEST_ROOT" + sep() + "Development" + sep()
	if r.Completed != want {
		t.Fatalf("Completed = %q, want %q", r.Completed, want)
	}
	if len(r.Candidates) != 1 || r.Candidates[0] != want {
		t.Fatalf("Candidates = %v, want [%q]", r.Candidates, want)
	}
}

func TestCompleteRanksDbExtensionsAboveOtherFiles(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"z.sqlite", "a.txt", "m.duckdb", "b.csv"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := Complete(root + sep())
	want := []string{"m.duckdb", "z.sqlite", "a.txt", "b.csv"}
	if len(r.Candidates) != len(want) {
		t.Fatalf("Candidates = %v, want %v", r.Candidates, want)
	}
	for i, w := range want {
		if r.Candidates[i] != root+sep()+w {
			t.Fatalf("Candidates = %v, want %v under %q", r.Candidates, want, root)
		}
	}
}

func TestCompleteRanksDirectoriesFirst(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "zdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.duckdb"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := Complete(root + sep())
	wantDir, wantFile := root+sep()+"zdir"+sep(), root+sep()+"a.duckdb"
	if len(r.Candidates) != 2 || r.Candidates[0] != wantDir || r.Candidates[1] != wantFile {
		t.Fatalf("Candidates = %v, want [%q, %q] (directory first despite name)", r.Candidates, wantDir, wantFile)
	}
}

func TestCompleteUnreadableDirNoCrash(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	in := blocked + sep() + "x"
	r := Complete(in)
	if r.Completed != in || r.Candidates != nil {
		t.Fatalf("unreadable dir should keep input and not crash: %+v", r)
	}
}

func TestCommonPrefixMixedCase(t *testing.T) {
	// F/f and B/b fold together and 'a' matches both; the divergence is r/z.
	if got := commonPrefix([]string{"Foobar", "fooBaz"}); !strings.EqualFold(got, "fooba") {
		t.Fatalf("commonPrefix = %q, want case-fold fooba", got)
	}
	if got := commonPrefix([]string{"abc"}); got != "abc" {
		t.Fatalf("commonPrefix single = %q", got)
	}
}

// TestCompleteFrom: a relative input resolves against the given
// base directory (not the process working directory) while candidates keep
// the typed relative notation; absolute inputs ignore the base.
func TestCompleteFrom(t *testing.T) {
	base := t.TempDir()
	for _, f := range []string{"leads.csv", "letters.txt"} {
		if err := os.WriteFile(filepath.Join(base, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(base, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	join := func(c []string) string { return strings.Join(c, ",") }

	if got := join(CompleteFrom(base, "le").Candidates); got != "leads.csv,letters.txt" {
		t.Fatalf("candidates = %q", got)
	}
	// Directories rank first: "lib/" sorts ahead of the two files even
	// though "leads.csv" is alphabetically first among all three names.
	dot := "." + sep()
	if got := join(CompleteFrom(base, dot+"l").Candidates); got != dot+"lib"+sep()+","+dot+"leads.csv,"+dot+"letters.txt" {
		t.Fatalf("./ candidates = %q", got)
	}
	if got := CompleteFrom(base, "").Candidates; len(got) != 3 {
		t.Fatalf("empty input must list the base dir, got %v", got)
	}

	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "abs.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := join(CompleteFrom(base, filepath.Join(other, "ab")).Candidates); got != filepath.Join(other, "abs.txt") {
		t.Fatalf("absolute candidates = %q", got)
	}
}
