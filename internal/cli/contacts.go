package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"unicode"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) runContacts(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return usageErr(errors.New("contacts supports export only"))
	}
	fs := flag.NewFlagSet("contacts export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "contacts", "export")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		contacts, err := st.Contacts(ctx)
		if err != nil {
			return err
		}
		export := control.ContactExport{Contacts: exportContacts(contacts)}
		if err := control.ValidateContactExport(export); err != nil {
			return err
		}
		return a.print(export)
	})
}

func exportContacts(contacts []store.Contact) []control.Contact {
	out := make([]control.Contact, 0, len(contacts))
	seen := map[string]struct{}{}
	for _, contact := range contacts {
		name := contactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		if name == "" || phone == "" {
			continue
		}
		key := name + "\x00" + phone
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, control.Contact{DisplayName: name, PhoneNumbers: []string{phone}})
	}
	return out
}

func contactDisplayName(contact store.Contact) string {
	for _, name := range []string{
		contact.FullName,
		contact.BusinessName,
		strings.TrimSpace(contact.FirstName + " " + contact.LastName),
	} {
		if cleaned := cleanContactName(name, contact); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func cleanContactName(name string, contact store.Contact) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ""
	case sameContactText(name, contact.Phone):
		return ""
	case sameContactText(name, contact.JID):
		return ""
	case sameContactText(name, contact.Username):
		return ""
	case sameContactText(name, contact.LID):
		return ""
	case strings.HasPrefix(name, "@"):
		return ""
	case looksLikePhone(name):
		return ""
	default:
		return name
	}
}

func sameContactText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}

func looksLikePhone(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
}
