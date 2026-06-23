package sqlitedsn

import (
	"net/url"
	"testing"
)

func TestFileBuildsSQLiteURI(t *testing.T) {
	got := File("/tmp/archive one.db", P("mode", "ro"), P("_pragma", "query_only(1)"))
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "file" || u.Path != "/tmp/archive one.db" {
		t.Fatalf("uri = %q", got)
	}
	query := u.Query()
	if query.Get("mode") != "ro" || query.Get("_pragma") != "query_only(1)" {
		t.Fatalf("query = %#v", query)
	}
}
