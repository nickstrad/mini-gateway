package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

const siteLinkProbe = `import sys
from pathlib import Path
sys.path[:0] = [sys.argv[1], sys.argv[2]]
import build_site as b
b.ROOT = Path(sys.argv[1])
for href in sys.argv[3:]:
    try:
        print(href, b.rewrite_links('<a href="%s">x</a>' % href, "docs/index.md", "index.html", {}) and "ok")
    except b.BuildError as e:
        print(href, e)
`

func TestSiteLinksMustNameFilesGitTracks(t *testing.T) {
	scripts, repo := filepath.Join("..", ".github", "scripts"), t.TempDir()
	setup := "cd " + repo + " && mkdir -p docs/gate docs/staged && touch markdown.py docs/tracked.md docs/gate/run.log docs/staged/a.md" +
		" && echo '*.log' > .gitignore && git init -q && git add -A"
	if out, err := exec.Command("bash", "-c", setup).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := exec.Command("python3", "-I", "-B", "-c", siteLinkProbe, repo, scripts, "tracked.md", "staged", "gate/run.log", "gate", "missing.md").CombinedOutput()
	want := "tracked.md ok\nstaged ok\ngate/run.log docs/index.md: link to a file git does not track: gate/run.log\n" +
		"gate docs/index.md: link to a file git does not track: gate\nmissing.md docs/index.md: broken link: missing.md\n"
	if err != nil || string(out) != want {
		t.Fatalf("%v\ngot:\n%s\nwant:\n%s", err, out, want)
	}
}
