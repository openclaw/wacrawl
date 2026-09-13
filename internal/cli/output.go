package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/openclaw/wacrawl/internal/backup"
	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) print(value any) error {
	if a.json {
		return a.printJSON(value)
	}
	switch v := value.(type) {
	case store.ImportStats:
		_, err := fmt.Fprintf(a.stdout, "mode=%s\nsource=%s\ndb=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\nmedia_copied=%d\nmedia_missing=%d\n",
			v.Mode, v.SourcePath, v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, v.MediaCopied, v.MediaMissing)
		return err
	case store.Status:
		_, err := fmt.Fprintf(a.stdout, "db=%s\nchats=%d\nunread_chats=%d\nunread_messages=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\ndeleted_chats=%d\ndeleted_contacts=%d\ndeleted_groups=%d\ndeleted_participants=%d\ndeleted_messages=%d\nmessage_revisions=%d\noldest=%s\nnewest=%s\nlast_import=%s\nsource=%s\n",
			v.DBPath, v.Chats, v.UnreadChats, v.UnreadMessages, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, v.DeletedChats, v.DeletedContacts, v.DeletedGroups, v.DeletedParticipants, v.DeletedMessages, v.MessageRevisions, formatTime(v.OldestMessage), formatTime(v.NewestMessage), formatTime(v.LastImportAt), v.LastSource)
		return err
	case []store.Chat:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "LAST\tKIND\tUNREAD\tMESSAGES\tJID\tNAME")
		for _, c := range v {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", formatTime(c.LastMessageAt), c.Kind, c.UnreadCount, c.MessageCount, c.JID, c.Name)
		}
		return tw.Flush()
	case []store.Message:
		for _, m := range v {
			body := firstNonEmpty(m.Snippet, m.Text, m.MediaTitle, m.MessageType)
			if _, err := fmt.Fprintf(a.stdout, "[%s] %s / %s / %s\n%s\n\n", formatTime(m.Timestamp), m.ChatName, firstNonEmpty(m.SenderName, m.SenderJID), m.MessageID, body); err != nil {
				return err
			}
		}
		return nil
	case sqlQueryResult:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(v.columns, "\t"))
		for _, row := range v.rows {
			values := make([]string, 0, len(v.columns))
			for _, col := range v.columns {
				values = append(values, formatSQLValue(row[col]))
			}
			_, _ = fmt.Fprintln(tw, strings.Join(values, "\t"))
		}
		return tw.Flush()
	case backup.Result:
		_, err := fmt.Fprintf(a.stdout, "repo=%s\nchanged=%t\nencrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\n", v.Repo, v.Changed, v.Encrypted, v.Shards, v.Messages, v.MediaFiles)
		if err == nil && v.Ref != "" {
			_, err = fmt.Fprintf(a.stdout, "ref=%s\n", v.Ref)
		}
		if err == nil && v.Tag != "" {
			_, err = fmt.Fprintf(a.stdout, "tag=%s\n", v.Tag)
		}
		return err
	case []backup.Snapshot:
		if len(v) == 0 {
			_, err := fmt.Fprintln(a.stdout, "No backup snapshots found.")
			return err
		}
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "REF\tEXPORTED\tMESSAGES\tMEDIA\tSHARDS\tTAGS")
		for _, snapshot := range v {
			ref := snapshot.Ref
			if len(ref) > 12 {
				ref = ref[:12]
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n", ref, formatTime(snapshot.Exported), snapshot.Counts.Messages, snapshot.Counts.MediaFiles, snapshot.Shards, strings.Join(snapshot.Tags, ","))
		}
		return tw.Flush()
	case backup.Manifest:
		_, err := fmt.Fprintf(a.stdout, "encrypted=%t\nshards=%d\nmessages=%d\nmedia_files=%d\nexported=%s\n", v.Encrypted, len(v.Shards), v.Counts.Messages, len(v.Files), formatTime(v.Exported))
		return err
	default:
		return a.printJSON(value)
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *app) printJSON(value any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
