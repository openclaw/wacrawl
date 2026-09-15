package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSearchDateFiltersAndOrder(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(24 * time.Hour)
	for _, row := range []struct {
		pk   int64
		ts   int64
		text string
	}{
		{1, 0, "launch unknown"},
		{2, -1, "launch negative"},
		{3, maxJSONUnixSecond + 1, "launch future"},
		{4, older.Unix(), "launch older with several other words here"},
		{5, newer.Unix(), "launch launch launch launch"},
		{6, newer.Unix(), "launch launch launch"},
	} {
		if _, err := st.DB().ExecContext(ctx, `insert into messages(source_pk,event_id,chat_jid,msg_id,ts,from_me,text,raw_type) values(?,?,'chat',?,?,0,?,0)`, row.pk, messageEventID(row.pk), messageEventID(row.pk), row.ts, row.text); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx, `insert into messages_fts(rowid,text) values(?,?)`, row.pk, row.text); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		filter MessageFilter
		want   []int64
	}{
		{"after excludes unknown dates", MessageFilter{After: &older}, []int64{5, 6, 4}},
		{"before excludes unknown dates", MessageFilter{Before: &newer}, []int64{5, 6, 4}},
		{"range includes endpoints", MessageFilter{After: &older, Before: &newer}, []int64{5, 6, 4}},
		{"ascending overrides relevance", MessageFilter{After: &older, Before: &newer, Asc: true}, []int64{4, 5, 6}},
		{"ascending retains unknown dates first", MessageFilter{Asc: true}, []int64{1, 2, 3, 4, 5, 6}},
		{"ascending limit selects oldest", MessageFilter{After: &older, Asc: true, Limit: 1}, []int64{4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := tc.filter
			filter.Query = "launch"
			got, err := st.Search(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			var pks []int64
			for _, message := range got {
				pks = append(pks, message.SourcePK)
			}
			if !reflect.DeepEqual(pks, tc.want) {
				t.Fatalf("search source keys = %v, want %v", pks, tc.want)
			}
		})
	}
	// Undated records remain searchable when no date filter is requested.
	got, err := st.Search(ctx, MessageFilter{Query: "unknown"})
	if err != nil || len(got) != 1 || got[0].SourcePK != 1 || !got[0].Timestamp.IsZero() {
		t.Fatalf("undated search = %+v, %v", got, err)
	}
}
