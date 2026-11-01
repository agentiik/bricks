package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentiik/bricks/internal/envelope"
	"github.com/agentiik/bricks/internal/render"
)

// message is one item's mail, built and ready to hand to the server.
type message struct {
	Item       envelope.Item
	ID         string
	From       *mail.Address
	To, Cc     []*mail.Address
	Bcc        []*mail.Address
	Subject    string
	Recipients []string
	Bytes      []byte
}

// now is the clock the Date header reads. A test sets it.
var now = time.Now

// compose fills the templates with one item and writes the message as RFC 5322 has it.
func compose(task envelope.Task, s settings, item envelope.Item, dir string) (message, error) {
	m := message{Item: item, From: s.From}
	var err error
	if m.To, err = addresses("to", s.To, item); err != nil {
		return m, err
	}
	if len(m.To) == 0 {
		return m, fmt.Errorf("to fills to no address")
	}
	if m.Cc, err = addresses("cc", s.Cc, item); err != nil {
		return m, err
	}
	if m.Bcc, err = addresses("bcc", s.Bcc, item); err != nil {
		return m, err
	}
	replyTo, err := addresses("reply_to", s.ReplyTo, item)
	if err != nil {
		return m, err
	}
	if m.Subject, err = render.Render("subject", s.Subject, item); err != nil {
		return m, err
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return m, fmt.Errorf("the subject fills to more than one line, and a header is one")
	}
	body, err := render.Render("body", s.Body, item)
	if err != nil {
		return m, err
	}
	for _, list := range [][]*mail.Address{m.To, m.Cc, m.Bcc} {
		for _, a := range list {
			m.Recipients = append(m.Recipients, a.Address)
		}
	}
	m.ID = messageID(task, item, s.From)

	var b bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&b, "%s: %s\r\n", name, value) }
	header("From", s.From.String())
	header("To", list(m.To))
	if len(m.Cc) > 0 {
		header("Cc", list(m.Cc))
	}
	if len(replyTo) > 0 {
		header("Reply-To", list(replyTo))
	}
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", now().UTC().Format(time.RFC1123Z))
	header("Message-ID", m.ID)
	header("MIME-Version", "1.0")

	kind := "text/plain; charset=utf-8"
	if s.HTML {
		kind = "text/html; charset=utf-8"
	}
	files := item.Files
	if !s.Attach {
		files = nil
	}
	if len(files) == 0 {
		header("Content-Type", kind)
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		writeQP(&b, body)
		m.Bytes = b.Bytes()
		return m, nil
	}

	boundary := "agk-" + strings.Trim(m.ID, "<>")
	boundary = boundary[:min(len(boundary), 60)]
	header("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": boundary}))
	fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
	header("Content-Type", kind)
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	writeQP(&b, body)
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(dir, in, f.Name))
		if err != nil {
			return m, fmt.Errorf("the attachment %s could not be read: %w", f.Name, err)
		}
		fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
		header("Content-Type", f.MediaType)
		header("Content-Transfer-Encoding", "base64")
		header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
		b.WriteString("\r\n")
		writeBase64(&b, content)
	}
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	m.Bytes = b.Bytes()
	return m, nil
}

// addresses fills one template and reads what it fills to as a list of addresses, empty where
// the step gave no template.
func addresses(what, text string, item envelope.Item) ([]*mail.Address, error) {
	if text == "" {
		return nil, nil
	}
	filled, err := render.Render(what, text, item)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(filled) == "" {
		return nil, nil
	}
	list, err := mail.ParseAddressList(filled)
	if err != nil {
		return nil, fmt.Errorf("%s fills to %q, which is not a list of addresses: %s", what, filled, err)
	}
	return list, nil
}

func list(as []*mail.Address) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// messageID is the same for one item of one step of one run on every attempt, so that a retry
// sending a message again sends it under the identity the first copy carried, and a receiver
// that keeps Message-IDs recognises it. The domain is the sender's, as RFC 5322 recommends.
func messageID(task envelope.Task, item envelope.Item, from *mail.Address) string {
	sum := sha256.Sum256([]byte(task.RunID + "\x00" + task.Step + "\x00" + item.ID))
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]
	return "<" + hex.EncodeToString(sum[:16]) + "@" + domain + ">"
}

func writeQP(b *bytes.Buffer, text string) {
	w := quotedprintable.NewWriter(b)
	w.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")))
	w.Close()
}

// writeBase64 wraps at 76 characters, the line length MIME sets for the encoding.
func writeBase64(b *bytes.Buffer, content []byte) {
	enc := base64.StdEncoding.EncodeToString(content)
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
}
