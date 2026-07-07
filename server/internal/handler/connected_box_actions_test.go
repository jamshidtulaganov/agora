package handler

import (
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The seed template is operator-defined; the per-box values it interpolates
// come from the DB and must enter the shell as DATA. A hostile work_dir must
// not break out of its argument.
func TestExpandBoxSeedCommand(t *testing.T) {
	box := db.ConnectedBox{WorkDir: "/var/www/jamshid.sdteam.uz/"}
	got := expandBoxSeedCommand("/usr/local/bin/box-seed {subdomain} --dir {work_dir}", box)
	want := "/usr/local/bin/box-seed 'jamshid.sdteam.uz' --dir '/var/www/jamshid.sdteam.uz'"
	if got != want {
		t.Fatalf("expand:\n got %q\nwant %q", got, want)
	}

	evil := db.ConnectedBox{WorkDir: "/var/www/x'; rm -rf / #"}
	got = expandBoxSeedCommand("seed {work_dir} {subdomain}", evil)
	if strings.Contains(got, "'; rm") && !strings.Contains(got, `'\''`) {
		t.Fatalf("quote breakout not neutralized: %q", got)
	}
	if !strings.HasPrefix(got, "seed '") {
		t.Fatalf("work_dir not quoted: %q", got)
	}
}
