package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const siteBookProbe = `import sys
from pathlib import Path
sys.path[:0] = [sys.argv[1], sys.argv[2]]
import build_site as b
b.ROOT = Path(sys.argv[3])
try:
    _, chapters = b.load_book()
    for ch in chapters:
        print(ch["status"], ch["out"], b.chapter_label(ch))
    print(b.book_contents(chapters).strip())
    for ch in chapters:
        if ch["status"] == "live":
            print(b.pages(chapters)[ch["file"]], b.chapter_footer(chapters, ch).strip())
except b.BuildError as e:
    print("error:", e)
`

type siteChapter map[string]any

func siteBook(t *testing.T, root string) string {
	t.Helper()
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "markdown.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	scripts, err := filepath.Abs(filepath.Join("..", ".github", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", "-I", "-B", "-c", siteBookProbe, stub, scripts, root).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return string(out)
}

func siteBookOf(t *testing.T, files []string, chapters ...siteChapter) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "book", "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, "book", "chapters", f), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := json.Marshal(map[string]any{"title": "the book", "chapters": chapters})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "book", "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return siteBook(t, root)
}

func siteEntry(number int, suffix any, file, status string) siteChapter {
	ch := siteChapter{"number": number, "chunk": number, "title": "T" + filepath.Base(file)[:3], "file": "book/chapters/" + file, "status": status}
	if suffix != nil {
		ch["suffix"] = suffix
	}
	return ch
}

// TestSiteBookSuffixedChapterIsOrderedAndLabelled proves suffixed chapters sort
// after their base number and get matching TOC and prev/next links.
func TestSiteBookSuffixedChapterIsOrderedAndLabelled(t *testing.T) {
	got := siteBookOf(t, []string{"06-skeleton.md", "06a-operator-tools.md", "06b-more.md"},
		siteEntry(7, nil, "07-pool.md", "planned"),
		siteEntry(6, "b", "06b-more.md", "live"),
		siteEntry(6, "a", "06a-operator-tools.md", "live"),
		siteEntry(0, nil, "00-preface.md", "planned"),
		siteEntry(6, nil, "06-skeleton.md", "live"))
	want := `planned book/00-preface.html T00-
live book/06-skeleton.html 6. T06-
live book/06a-operator-tools.html 6a. T06a
live book/06b-more.html 6b. T06b
planned book/07-pool.html 7. T07-
<h2 id="contents">Contents</h2>
<ul class="book-toc">
<li class="planned">T00- (planned)</li>
<li><a href="06-skeleton.html">6. T06-</a></li>
<li><a href="06a-operator-tools.html">6a. T06a</a></li>
<li><a href="06b-more.html">6b. T06b</a></li>
<li class="planned">7. T07- (planned)</li>
</ul>
book/06-skeleton.html <nav class="chapter"><a href="index.html">Contents</a><a href="06a-operator-tools.html">Next: 6a. T06a</a></nav>
book/06a-operator-tools.html <nav class="chapter"><a href="06-skeleton.html">Previous: 6. T06-</a><a href="index.html">Contents</a><a href="06b-more.html">Next: 6b. T06b</a></nav>
book/06b-more.html <nav class="chapter"><a href="06a-operator-tools.html">Previous: 6a. T06a</a><a href="index.html">Contents</a></nav>
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSiteBookSuffixErrors proves build_site.py refuses malformed, mismatched,
// duplicate, missing-live, or present-planned suffixed manifest entries.
func TestSiteBookSuffixErrors(t *testing.T) {
	const where = "error: book/manifest.json: chapter entry 2: "
	six := siteEntry(6, nil, "06-skeleton.md", "planned")
	fileMsg := func(suffix, file string) string {
		return where + `"file" must be book/chapters/NN` + suffix + `-<slug>.md with NN equal to the number and followed by the suffix, got 'book/chapters/` + file + "'\n"
	}
	for _, c := range []struct {
		name   string
		second siteChapter
		files  []string
		want   string
	}{
		{"upper-case suffix", siteEntry(6, "A", "06a-tools.md", "planned"), nil, where + `"suffix" must be one lowercase letter (a to z), got 'A'` + "\n"},
		{"two-letter suffix", siteEntry(6, "ab", "06ab-tools.md", "planned"), nil, where + `"suffix" must be one lowercase letter (a to z), got 'ab'` + "\n"},
		{"empty suffix", siteEntry(6, "", "06-tools.md", "planned"), nil, where + `"suffix" must be one lowercase letter (a to z), got ''` + "\n"},
		{"digit suffix", siteEntry(6, "1", "061-tools.md", "planned"), nil, where + `"suffix" must be one lowercase letter (a to z), got '1'` + "\n"},
		{"numeric suffix", siteEntry(6, 1, "06a-tools.md", "planned"), nil, where + `"suffix" must be one lowercase letter (a to z), got 1` + "\n"},
		{"suffix missing from the file", siteEntry(6, "a", "06-tools.md", "planned"), nil, fileMsg("a", "06-tools.md")},
		{"another suffix in the file", siteEntry(6, "a", "06b-tools.md", "planned"), nil, fileMsg("a", "06b-tools.md")},
		{"another number in the file", siteEntry(6, "a", "07a-tools.md", "planned"), nil, fileMsg("a", "07a-tools.md")},
		{"suffix in the file only", siteEntry(6, nil, "06a-tools.md", "planned"), nil,
			where + `"file" must be book/chapters/NN-<slug>.md with NN equal to the number, got 'book/chapters/06a-tools.md'` + "\n"},
		{"plain number twice", siteEntry(6, nil, "06-again.md", "planned"), nil, where + "chapter number or file listed twice\n"},
		{"live and missing", siteEntry(6, "a", "06a-tools.md", "live"), nil,
			"error: book/manifest.json: chapter 6a is live but book/chapters/06a-tools.md is missing\n"},
		{"planned and present", siteEntry(6, "a", "06a-tools.md", "planned"), []string{"06a-tools.md"},
			"error: book/manifest.json: chapter 6a is planned but book/chapters/06a-tools.md exists (set its status to live, or remove the file)\n"},
	} {
		if got := siteBookOf(t, c.files, six, c.second); got != c.want {
			t.Errorf("%s:\ngot:  %swant: %s", c.name, got, c.want)
		}
	}
	got := siteBookOf(t, nil, six, siteEntry(6, "a", "06a-tools.md", "planned"), siteEntry(6, "a", "06a-other.md", "planned"))
	if want := "error: book/manifest.json: chapter entry 3: chapter number or file listed twice\n"; got != want {
		t.Errorf("number and suffix twice:\ngot:  %swant: %s", got, want)
	}
}

// TestSiteBookManifestListsChapter6aBetween6And7 proves book/manifest.json
// places chapter 6a immediately between chapters 6 and 7.
func TestSiteBookManifestListsChapter6aBetween6And7(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	got := siteBook(t, root)
	if strings.HasPrefix(got, "error:") {
		t.Fatal(got)
	}
	var labels []string
	for _, line := range strings.Split(got, "\n") {
		if f := strings.SplitN(line, " ", 4); len(f) == 4 && (f[0] == "planned" || f[0] == "live") {
			labels = append(labels, f[1]+" "+f[2])
		}
	}
	joined := strings.Join(labels, "\n")
	want := "book/06-walking-skeleton.html 6.\nbook/06a-operator-tools.html 6a.\nbook/07-pool.html 7."
	if !strings.Contains(joined, want) {
		t.Fatalf("chapter 6a is not between 6 and 7:\n%s", joined)
	}
}
