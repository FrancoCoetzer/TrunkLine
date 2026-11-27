package svn

import (
	"fmt"
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

func TestParseConflicts(t *testing.T) {
	text := "a\r\nb\r\n<<<<<<< .mine\r\nmine 1\r\nmine 2\r\n||||||| .r1\r\nold\r\n=======\r\ntheirs\r\n>>>>>>> .r3\r\nc\r\n" +
		"<<<<<<< .mine\r\n=======\r\nonly theirs\r\n>>>>>>> .r3\r\n<<<<<<< not closed\r\nd\r\n"
	chunks, eol, final := ParseConflicts(text)
	if eol != "\r\n" || !final {
		t.Errorf("eol %q, final %v", eol, final)
	}
	want := []Chunk{
		{Text: []string{"a", "b"}},
		{Conflict: true, Mine: []string{"mine 1", "mine 2"}, Base: []string{"old"}, Theirs: []string{"theirs"}, MineLabel: ".mine", TheirsLabel: ".r3"},
		{Text: []string{"c"}},
		{Conflict: true, Theirs: []string{"only theirs"}, MineLabel: ".mine", TheirsLabel: ".r3"},
		{Text: []string{"<<<<<<< not closed", "d"}},
	}
	if fmt.Sprintf("%+v", chunks) != fmt.Sprintf("%+v", want) {
		t.Errorf("got  %+v\nwant %+v", chunks, want)
	}
	if c, eol, final := ParseConflicts("x\ny"); len(c) != 1 || eol != "\n" || final {
		t.Errorf("plain: %+v %q %v", c, eol, final)
	}
}
