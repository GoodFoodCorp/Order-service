// Package mailer sends the invoices by SMTP (standard library only):
// STARTTLS when the server offers it, implicit TLS on port 465.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"goodfood/order-service/internal/adapter/invoicedoc"
	"goodfood/order-service/internal/domain"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	// FrontendURL links the email to the order page.
	FrontendURL string
}

type SMTP struct{ cfg Config }

func New(cfg Config) *SMTP {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &SMTP{cfg: cfg}
}

func (m *SMTP) SendInvoice(ctx context.Context, inv *domain.Invoice) error {
	html, err := invoicedoc.HTML(inv)
	if err != nil {
		return err
	}
	link := strings.TrimRight(m.cfg.FrontendURL, "/") + "/orders/" + inv.OrderID
	text := invoicedoc.Text(inv) + "\nVotre commande : " + link + "\n"
	subject := fmt.Sprintf("Votre facture Good Food %s", inv.Number)
	return m.send(ctx, inv.CustomerEmail, subject, string(html), text)
}

func (m *SMTP) send(ctx context.Context, to, subject, html, text string) error {
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("invalid recipient")
	}
	boundary := randomHex(12)
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		m.cfg.From, to, mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@goodfood>\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", randomHex(16), boundary)
	for _, part := range []struct{ kind, body string }{{"text/plain", text}, {"text/html", html}} {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.kind)
		qp := quotedprintable.NewWriter(&stringWriter{&b})
		_, _ = qp.Write([]byte(part.body))
		_ = qp.Close()
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if m.cfg.Port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && m.cfg.Port != 465 {
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if m.cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", m.cfg.User, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(addressOf(m.cfg.From)); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(b.String())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// addressOf takes the address out of "Good Food <no-reply@…>".
func addressOf(from string) string {
	if i, j := strings.LastIndex(from, "<"), strings.LastIndex(from, ">"); i >= 0 && j > i {
		return from[i+1 : j]
	}
	return from
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
