// Command smtp-send sends one message per item, its recipients, subject and body filled from the
// item by templates, and the files the item carries attached.
//
// The server and its account are the secret smtp and never parameters, since a parameter is
// printed in a run's detail and the account carries a password. A message goes out over TLS or
// not at all: an smtps:// server is reached over TLS from the first byte, and an smtp:// server
// has to offer STARTTLS, since a password and the mail itself would otherwise cross the network
// as they are.
//
// A message the server refuses is published on error with the server's reply, and the rest of
// the batch is sent: one bad address in a thousand is not a failed step, and the workflow decides
// what it is worth. A server that cannot be reached, or refuses the account, is a failed step
// before anything was sent, and a transient one where the server may answer next time.
//
// Sending cannot be undone, so a retry of the step sends again what the first attempt sent. Each
// message's Message-ID is derived from the run, the step and the item, the same on every
// attempt, which is what lets a receiving system recognise the second copy as the first.
package main

import (
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"

	"github.com/agentiik/bricks/internal/envelope"
	"github.com/agentiik/bricks/internal/render"
)

const (
	in   = "in"
	out  = "out"
	fail = "error"

	// smtpPath is where the runner binds the secret of that name: the manifest's own mount point.
	smtpPath = "/agk/secrets/smtp"

	// inDir is where the artifacts of an input port are laid down, beside that port's envelope.
	inDir = "/agk/in"
)

// settings is what the step supplied. Every field but From is a template filled per item.
type settings struct {
	From    *mail.Address
	To      string
	Cc      string
	Bcc     string
	ReplyTo string
	Subject string
	Body    string
	HTML    bool
	Attach  bool
}

func main() {
	task := envelope.Read()

	s, err := configure()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	// Every message is built before the server is reached, so that an item the templates cannot
	// fill refuses the step before anything was sent rather than after half of it was.
	msgs := make([]message, 0, len(batch.Items))
	for _, item := range batch.Items {
		m, err := compose(task, s, item, inDir)
		if err != nil {
			envelope.Die(envelope.Invalid, "item %s: %s", item.ID, err)
		}
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "0 items in, nothing sent")
		return
	}

	secret, err := os.ReadFile(smtpPath)
	if errors.Is(err, os.ErrNotExist) {
		envelope.Die(envelope.Invalid, "no secret smtp is mounted at %s: smtp-send reads the server and its account from it, and the workflow names the secret of its namespace that answers", smtpPath)
	}
	if err != nil {
		envelope.Die(envelope.Failed, "the secret smtp could not be read: %s", err)
	}
	srv, err := server(strings.TrimSpace(string(secret)))
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	sent, refused, err := send(srv, msgs)
	var unreachable *unreachableError
	switch {
	case errors.As(err, &unreachable):
		envelope.Die(envelope.Transient, "%s", err)
	case err != nil:
		envelope.Die(envelope.Failed, "%s", err)
	}
	if err := task.Out(out, sent); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	if err := task.Out(fail, refused); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%d items in, %d sent, %d refused\n", len(batch.Items), len(sent), len(refused))
}

// configure reads the step's parameters, and parses every template once so that one that does
// not parse is refused before any item is read.
func configure() (settings, error) {
	s := settings{Attach: true}
	params, err := envelope.Params()
	if err != nil {
		return s, err
	}
	var from, bodyPath string
	for name, into := range map[string]any{
		"from": &from, "to": &s.To, "cc": &s.Cc, "bcc": &s.Bcc, "reply_to": &s.ReplyTo,
		"subject": &s.Subject, "body": &s.Body, "body_path": &bodyPath, "html": &s.HTML, "attach": &s.Attach,
	} {
		if _, err := envelope.Into(params, name, into); err != nil {
			return s, err
		}
	}
	if from == "" {
		return s, errors.New("no from: smtp-send sends as one address, and the step names it")
	}
	if s.From, err = mail.ParseAddress(from); err != nil {
		return s, fmt.Errorf("from is %q, which is not an address: %s", from, err)
	}
	if s.To == "" {
		return s, errors.New("no to: smtp-send sends each item's message to the addresses to names, usually from the item, as {{.email}}")
	}
	if s.Subject == "" {
		return s, errors.New("no subject: a message without one is one a person files as spam")
	}
	switch {
	case s.Body != "" && bodyPath != "":
		return s, errors.New("body and body_path were both supplied: a step gives the body inline or names a file of the repository, and one of the two")
	case bodyPath != "":
		if s.Body, err = render.Read("body_path", bodyPath); err != nil {
			return s, err
		}
	case s.Body == "":
		return s, fmt.Errorf("no body: a message's body is given inline as body or as body_path relative to %s", render.RepoDir)
	}
	for what, text := range map[string]string{"to": s.To, "cc": s.Cc, "bcc": s.Bcc, "reply_to": s.ReplyTo, "subject": s.Subject, "body": s.Body} {
		if _, err := render.Parse(what, text, ""); err != nil {
			return s, err
		}
	}
	return s, nil
}
