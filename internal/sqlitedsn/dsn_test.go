package sqlitedsn

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileBuildsAbsoluteSQLiteURI(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "relative", path: "./relative one?#.db", want: filepath.Join(root, "relative one?#.db")},
		{name: "absolute", path: filepath.Join(root, "absolute one?#.db"), want: filepath.Join(root, "absolute one?#.db")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := File(tc.path, P("mode", "ro"), P("_pragma", "query_only(1)"))
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			wantURIPath := filepath.ToSlash(tc.want)
			if runtime.GOOS == "windows" && filepath.VolumeName(tc.want) != "" && !strings.HasPrefix(wantURIPath, "/") {
				wantURIPath = "/" + wantURIPath
			}
			if u.Scheme != "file" || u.Host != "" || u.Path != wantURIPath {
				t.Fatalf("uri = %q, path = %q, want %q", got, u.Path, wantURIPath)
			}
			query := u.Query()
			if query.Get("mode") != "ro" || query.Get("_pragma") != "query_only(1)" {
				t.Fatalf("query = %#v", query)
			}
		})
	}
}

func TestFilePreservesInMemoryDatabase(t *testing.T) {
	got, err := File(":memory:", P("cache", "shared"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "file::memory:?cache=shared" {
		t.Fatalf("uri = %q", got)
	}
}
