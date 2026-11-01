package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentiik/bricks/internal/envelope"
)

// What the brick does with a batch, held against a server in the test's own process that speaks
// as much SMTP as the brick uses: EHLO, STARTTLS, AUTH PLAIN, MAIL, RCPT, DATA, RSET and QUIT.
// The contract half, what a container is given and what it writes, is the cases'.

// fake is that server, and what it was handed.
type fake struct {
	addr     string
	cert     *x509.Certificate
	key      *tls.Certificate
	plain    bool              // offers no STARTTLS
	refuse   map[string]string // a recipient and the reply that refuses it
	password string

	mu       sync.Mutex
	received []delivery
}

type delivery struct {
	from string
	to   []string
	data string
}

func serve(t *testing.T, f *fake) *fake {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	f.cert, _ = x509.ParseCertificate(der)
	f.key = &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	f.addr = l.Addr().String()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go f.session(conn)
		}
	}()
	return f
}

func (f *fake) session(conn net.Conn) {
	defer conn.Close()
	r, w := bufio.NewReader(conn), conn
	say := func(s string) { io.WriteString(w, s+"\r\n") }
	say("220 fake ready")
	secure := false
	var d delivery
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO":
			if !secure && !f.plain {
				say("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN")
			} else {
				say("250-fake\r\n250 AUTH PLAIN")
			}
		case "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*f.key}})
			if tc.Handshake() != nil {
				return
			}
			conn, r, w, secure = tc, bufio.NewReader(tc), tc, true
			say = func(s string) { io.WriteString(w, s+"\r\n") }
		case "AUTH":
			b, _ := base64.StdEncoding.DecodeString(strings.Fields(line)[2])
			if parts := strings.Split(string(b), "\x00"); len(parts) == 3 && parts[2] == f.password {
				say("235 accepted")
			} else {
				say("535 5.7.8 authentication failed")
			}
		case "MAIL":
			d = delivery{from: between(line)}
			say("250 ok")
		case "RCPT":
			to := between(line)
			if reply, ok := f.refuse[to]; ok {
				say(reply)
				continue
			}
			d.to = append(d.to, to)
			say("250 ok")
		case "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			d.data = b.String()
			f.mu.Lock()
			f.received = append(f.received, d)
			f.mu.Unlock()
			say("250 queued")
		case "RSET":
			d = delivery{}
			say("250 ok")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

func between(line string) string {
	return line[strings.Index(line, "<")+1 : strings.LastIndex(line, ">")]
}

func (f *fake) endpoint() endpoint {
	host, port, _ := net.SplitHostPort(f.addr)
	pool := x509.NewCertPool()
	pool.AddCert(f.cert)
	return endpoint{Addr: net.JoinHostPort(host, port), Host: host, User: "billing", Password: "s3cret", TLS: &tls.Config{RootCAs: pool}}
}

var invoice = settings{
	From:    &mail.Address{Name: "Billing", Address: "billing@example.com"},
	To:      "{{.email}}",
	Cc:      "{{.manager}}",
	Bcc:     "archive@example.com",
	Subject: "Facture {{.ref}} échue",
	Body:    "Dear {{.name}},\n\nInvoice {{.ref}} is attached.\n",
	Attach:  true,
}

func composed(t *testing.T, s settings, items ...envelope.Item) []message {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, in), 0o755)
	os.WriteFile(filepath.Join(dir, in, "a1.pdf"), []byte("%PDF-a1"), 0o644)
	task := envelope.Task{RunID: "01JMZ8W4K2R7Q0E3N5T9ZQ4XKB", Step: "remind"}
	var out []message
	for _, item := range items {
		m, err := compose(task, s, item, dir)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func customer(id, email string, files ...envelope.File) envelope.Item {
	return envelope.Item{ID: id, Data: map[string]any{"email": email, "manager": "", "name": "Ada", "ref": id}, Files: append([]envelope.File{}, files...)}
}

func TestEachItemIsSentAsOneMessage(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 10, 1, 4, 3, 13, 0, time.UTC) }
	f := serve(t, &fake{password: "s3cret"})
	pdf := envelope.File{Name: "a1.pdf", MediaType: "application/pdf"}
	msgs := composed(t, invoice, customer("a1", "ada@example.com", pdf), customer("b2", "Bob <bob@example.com>"))

	sent, refused, err := send(f.endpoint(), msgs)
	if err != nil || len(refused) != 0 {
		t.Fatalf("sent with %v, refused %v", err, refused)
	}
	if len(sent) != 2 || sent[0].ID != "a1" || !reflect.DeepEqual(sent[0].Data["to"], []any{"ada@example.com"}) {
		t.Fatalf("published %v", sent)
	}
	if len(f.received) != 2 {
		t.Fatalf("the server received %d messages", len(f.received))
	}
	got := f.received[0]
	if got.from != "billing@example.com" || !reflect.DeepEqual(got.to, []string{"ada@example.com", "archive@example.com"}) {
		t.Errorf("the envelope is from %s to %v", got.from, got.to)
	}
	m, err := mail.ReadMessage(strings.NewReader(got.data))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	for name, want := range map[string]string{"From": `"Billing" <billing@example.com>`, "To": "<ada@example.com>", "Bcc": "", "Cc": "", "Date": "Thu, 01 Oct 2026 04:03:13 +0000"} {
		if h := m.Header.Get(name); h != want {
			t.Errorf("%s is %q, and want %q", name, h, want)
		}
	}
	if subject != "Facture a1 échue" {
		t.Errorf("the subject reads %q", subject)
	}
	if id := m.Header.Get("Message-ID"); id != msgs[0].ID || !strings.HasSuffix(id, "@example.com>") || sent[0].Data["message_id"] != id {
		t.Errorf("the Message-ID is %q, published as %v", id, sent[0].Data["message_id"])
	}

	_, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	mr := multipart.NewReader(m.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		if p.Header.Get("Content-Transfer-Encoding") == "base64" {
			b, _ = base64.StdEncoding.DecodeString(string(b))
		}
		parts = append(parts, p.Header.Get("Content-Type")+" "+p.FileName()+" "+string(b))
	}
	want := []string{"text/plain; charset=utf-8  Dear Ada,\r\n\r\nInvoice a1 is attached.\r\n", "application/pdf a1.pdf %PDF-a1"}
	if !reflect.DeepEqual(parts, want) {
		t.Errorf("the parts are %q, and want %q", parts, want)
	}
}

func TestTheSameItemIsSentUnderTheSameIdentityOnEveryAttempt(t *testing.T) {
	a := composed(t, invoice, customer("a1", "ada@example.com"))[0].ID
	b := composed(t, invoice, customer("a1", "ada@example.com"))[0].ID
	c := composed(t, invoice, customer("b2", "ada@example.com"))[0].ID
	if a != b || a == c {
		t.Errorf("Message-IDs %s, %s for one item and %s for another", a, b, c)
	}
}

func TestARefusedRecipientIsPublishedOnErrorAndTheRestSent(t *testing.T) {
	f := serve(t, &fake{password: "s3cret", refuse: map[string]string{"gone@example.com": "550 5.1.1 no such user"}})
	sent, refused, err := send(f.endpoint(), composed(t, invoice, customer("a1", "gone@example.com"), customer("b2", "bob@example.com")))
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].ID != "b2" {
		t.Errorf("sent %v", sent)
	}
	if len(refused) != 1 || refused[0].ID != "a1" || refused[0].Data["code"] != 550 || refused[0].Data["message"] != "5.1.1 no such user" {
		t.Errorf("refused %v", refused)
	}
}

func TestNothingIsSentInTheClear(t *testing.T) {
	f := serve(t, &fake{password: "s3cret", plain: true})
	_, _, err := send(f.endpoint(), composed(t, invoice, customer("a1", "ada@example.com")))
	if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
		t.Errorf("a server without STARTTLS was answered with %v", err)
	}
	if len(f.received) != 0 {
		t.Error("a message was handed to it")
	}
}

func TestWhatNoRetryMendsIsToldFromWhatOneMight(t *testing.T) {
	f := serve(t, &fake{password: "other"})
	_, _, err := send(f.endpoint(), composed(t, invoice, customer("a1", "ada@example.com")))
	var unreachable *unreachableError
	if err == nil || errors.As(err, &unreachable) || !strings.Contains(err.Error(), "refused the account billing") {
		t.Errorf("a wrong password was answered with %v", err)
	}

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().String()
	l.Close()
	e := f.endpoint()
	e.Addr = closed
	if _, _, err := send(e, nil); !errors.As(err, &unreachable) {
		t.Errorf("a server that is not there was answered with %v, and want a transient failure", err)
	}
}

func TestATemplateThatCannotBeFilledIsRefused(t *testing.T) {
	task := envelope.Task{RunID: "r", Step: "s"}
	for _, c := range []struct {
		s    settings
		data map[string]any
		says string
	}{
		{invoice, map[string]any{"email": "ada@example.com"}, "cannot be filled"},
		{invoice, map[string]any{"email": "not an address", "manager": "", "name": "x", "ref": "x"}, "not a list of addresses"},
		{settings{From: invoice.From, To: "{{.email}}", Subject: "{{.s}}", Body: "x"}, map[string]any{"email": "a@example.com", "s": "two\nlines"}, "more than one line"},
	} {
		_, err := compose(task, c.s, envelope.Item{ID: "x", Data: c.data}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("got %v, and want a refusal saying %q", err, c.says)
		}
	}
}

func TestTheSecretIsReadAsAURL(t *testing.T) {
	for _, c := range []struct {
		secret string
		want   endpoint
	}{
		{"smtps://billing:s3cret@smtp.example.com", endpoint{Addr: "smtp.example.com:465", Host: "smtp.example.com", Implicit: true, User: "billing", Password: "s3cret"}},
		{"smtp://billing:p%40ss@smtp.example.com:2525", endpoint{Addr: "smtp.example.com:2525", Host: "smtp.example.com", User: "billing", Password: "p@ss"}},
		{"smtp://relay.internal", endpoint{Addr: "relay.internal:587", Host: "relay.internal"}},
	} {
		got, err := server(c.secret)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s is read as %+v, %v", c.secret, got, err)
		}
	}
	for _, c := range []struct{ secret, says string }{
		{"billing:s3cret@smtp.example.com:465", "not a URL"},
		{"http://smtp.example.com", "the scheme http"},
		{"smtp://smtp.example.com?insecure=true", "path or parameters"},
	} {
		_, err := server(c.secret)
		if err == nil || !strings.Contains(err.Error(), c.says) || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: got %v, and want a refusal saying %q without the password", c.secret, err, c.says)
		}
	}
}
