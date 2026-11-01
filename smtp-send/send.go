package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"time"

	"github.com/agentiik/bricks/internal/envelope"
)

// endpoint is the secret read: where the server is, how TLS starts, and the account.
type endpoint struct {
	Addr     string
	Host     string
	Implicit bool
	User     string
	Password string

	// TLS is nil in a container, which is the system's authorities and nothing loosened. A test
	// sets it to trust the certificate of the server it started.
	TLS *tls.Config
}

// unreachableError is a server that did not answer, or answered that it could not take mail now:
// the far side having a bad minute, worth a retry, and said before any message was sent.
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return "the server could not be reached: " + e.err.Error() }

// server reads the secret as a URL: smtps://user:password@host:465 for TLS from the first byte,
// smtp://user:password@host:587 for STARTTLS. The account is optional, for a relay that takes
// mail from the network it trusts without one.
func server(secret string) (endpoint, error) {
	u, err := url.Parse(secret)
	// A form that is not a URL parses as a scheme named after its first word and an opaque rest,
	// and naming that scheme back could print part of the credential.
	if err != nil || u.Scheme == "" || u.Opaque != "" || u.Hostname() == "" {
		return endpoint{}, errors.New("the secret smtp is not a URL: it is smtps://user:password@host:465 or smtp://user:password@host:587")
	}
	e := endpoint{Host: u.Hostname(), User: u.User.Username()}
	e.Password, _ = u.User.Password()
	port := u.Port()
	switch u.Scheme {
	case "smtps":
		e.Implicit = true
		if port == "" {
			port = "465"
		}
	case "smtp":
		if port == "" {
			port = "587"
		}
	default:
		return endpoint{}, fmt.Errorf("the secret smtp names the scheme %s, and smtp-send reads smtps and smtp", u.Scheme)
	}
	if len(u.Query()) > 0 || u.Path != "" && u.Path != "/" {
		return endpoint{}, errors.New("the secret smtp carries a path or parameters, and smtp-send reads a scheme, an account, a host and a port")
	}
	e.Addr = net.JoinHostPort(e.Host, port)
	return e, nil
}

// send hands every message to the server over one connection, and returns the items it took and
// the items it refused.
func send(e endpoint, msgs []message) (sent, refused []envelope.Item, err error) {
	c, err := connect(e)
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()

	for i, m := range msgs {
		if err := deliver(c, m); err != nil {
			var reply *textproto.Error
			if !errors.As(err, &reply) {
				// The connection itself went, and the messages not yet handed over were not
				// sent: each is published on error saying so, rather than the step failing with
				// the first part of its batch delivered and nothing saying which.
				for _, rest := range msgs[i:] {
					refused = append(refused, trouble(rest, 0, "the connection to the server was lost before this message was handed over: "+err.Error()))
				}
				return sent, refused, nil
			}
			refused = append(refused, trouble(m, reply.Code, reply.Msg))
			// The server holds the refused transaction open until it is reset.
			if err := c.Reset(); err != nil {
				for _, rest := range msgs[i+1:] {
					refused = append(refused, trouble(rest, 0, "the connection to the server was lost before this message was handed over: "+err.Error()))
				}
				return sent, refused, nil
			}
			continue
		}
		sent = append(sent, envelope.Item{ID: m.Item.ID, Data: map[string]any{
			"message_id": m.ID,
			"from":       m.From.Address,
			"to":         plain(m.To),
			"cc":         plain(m.Cc),
			"bcc":        plain(m.Bcc),
			"subject":    m.Subject,
		}, Files: []envelope.File{}})
	}
	// Every message was taken by then, so a QUIT the server did not answer changes nothing.
	c.Quit()
	return sent, refused, nil
}

// connect opens the connection, starts TLS and signs in, refusing a server that will not speak
// TLS.
func connect(e endpoint) (*smtp.Client, error) {
	cfg := e.TLS
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cfg = cfg.Clone()
	cfg.ServerName = e.Host
	dialer := &net.Dialer{Timeout: 30 * time.Second}

	var conn net.Conn
	var err error
	if e.Implicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", e.Addr, cfg)
	} else {
		conn, err = dialer.Dial("tcp", e.Addr)
	}
	if err != nil {
		return nil, &unreachableError{err}
	}
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		conn.Close()
		return nil, answered(err)
	}
	if err := c.Hello("localhost"); err != nil {
		c.Close()
		return nil, answered(err)
	}
	if !e.Implicit {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return nil, fmt.Errorf("%s does not offer STARTTLS, and smtp-send sends nothing in the clear: the account's password and the mail itself would cross the network as they are", e.Addr)
		}
		if err := c.StartTLS(cfg); err != nil {
			c.Close()
			return nil, fmt.Errorf("TLS with %s could not be started: %w", e.Addr, err)
		}
	}
	if e.User != "" {
		if err := c.Auth(smtp.PlainAuth("", e.User, e.Password, e.Host)); err != nil {
			c.Close()
			return nil, fmt.Errorf("%s refused the account %s: %w", e.Addr, e.User, err)
		}
	}
	return c, nil
}

// answered reads a reply the server gave before any message: 4xx is a server that cannot take
// mail now, and anything else is a fault no retry mends.
func answered(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 500 {
		return &unreachableError{err}
	}
	return err
}

// deliver hands one message over: its sender, each recipient, its bytes.
func deliver(c *smtp.Client, m message) error {
	if err := c.Mail(m.From.Address); err != nil {
		return err
	}
	for _, r := range m.Recipients {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(m.Bytes); err != nil {
		return err
	}
	return w.Close()
}

// trouble is a message the server did not take, under the identity of the item that asked for
// it, with the server's code where it gave one.
func trouble(m message, code int, msg string) envelope.Item {
	data := map[string]any{"message_id": m.ID, "to": plain(m.To), "subject": m.Subject, "message": msg}
	if code != 0 {
		data["code"] = code
	}
	return envelope.Item{ID: m.Item.ID, Data: data, Files: []envelope.File{}}
}

func plain(as []*mail.Address) []any {
	out := make([]any, len(as))
	for i, a := range as {
		out[i] = a.Address
	}
	return out
}
