package svn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConflictMarkers(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, text string
		want       int
	}{
		{"svn3way.c", "int a;\n<<<<<<< .mine\nmine\n||||||| .r8\nbase\n=======\ntheirs\n>>>>>>> .r9\n", 2},
		{"twoway.c", "x\r\n<<<<<<< .working\r\nmine\r\n=======\r\ntheirs\r\n>>>>>>> .merge-right.r9\r\n", 2},
		{"bare.txt", "<<<<<<<\na\n=======\nb\n>>>>>>>\n", 1},
		{"heading.md", "Title\n=======\n\ntext\n", 0},
		{"unclosed.txt", "<<<<<<< .mine\na\n=======\nb\n", 0},
		{"wrongorder.txt", ">>>>>>> x\n=======\n<<<<<<< y\n", 0},
		{"indented.txt", "  <<<<<<< .mine\n  =======\n  >>>>>>> .r9\n", 0},
		{"lookalike.txt", "<<<<<<<<< not a marker\n=======\n>>>>>>>>\n", 0},
		{"clean.c", "int main(void) { return 0; }\n", 0},
		{"binary.bin", "\x00<<<<<<< .mine\n=======\n>>>>>>> .r9\n", 0},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := ConflictMarkers(p); err != nil || got != c.want {
			t.Errorf("%s: line %d, %v; want %d", c.name, got, err, c.want)
		}
	}

	// Folders are searched, .svn is skipped.
	sub := filepath.Join(dir, "tree")
	write(t, filepath.Join(sub, "ok.c"), "fine\n")
	write(t, filepath.Join(sub, "deep", "bad.c"), "<<<<<<< .mine\n=======\n>>>>>>> .r2\n")
	write(t, filepath.Join(sub, ".svn", "pristine", "x"), "<<<<<<< .mine\n=======\n>>>>>>> .r2\n")
	found := FilesWithConflictMarkers([]string{sub, filepath.Join(dir, "clean.c"), filepath.Join(dir, "gone.c")})
	if len(found) != 1 || !strings.HasSuffix(found[0], filepath.Join("deep", "bad.c")+":1") {
		t.Errorf("found = %v", found)
	}
}
