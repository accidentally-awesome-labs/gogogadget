// Package smtp implements the self-hosted SMTP mail adapter.
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
	"net"
	"net/smtp"
	"strconv"
)

type Deps struct {
	Config *config.Config
}

type Module struct {
	Sender mail.Sender
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("mail smtp: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host := d.Config.Value("SMTP_HOST")
	if host == "" {
		return nil, fmt.Errorf("mail smtp: SMTP_HOST is not configured")
	}
	port, err := d.Config.IntValue("SMTP_PORT")
	if err != nil {
		return nil, fmt.Errorf("mail smtp: SMTP_PORT is invalid: %w", err)
	}
	username := d.Config.Value("SMTP_USERNAME")
	password := d.Config.Value("SMTP_PASSWORD")
	return &Module{Sender: NewSMTPSender(host, port, username, password, d.Config.EmailFrom)}, nil
}

type SMTPSender struct {
	host, username, password, from string
	port                           int
	dialAddr                       string
	tlsConfig                      *tls.Config
}

func NewSMTPSender(host string, port int, username, password, from string) *SMTPSender {
	return &SMTPSender{host: host, port: port, username: username, password: password, from: from, dialAddr: host}
}

func (s *SMTPSender) Send(ctx context.Context, msg mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	address := net.JoinHostPort(s.dialAddr, strconv.Itoa(s.port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsOptions := s.tlsConfig
		if tlsOptions == nil {
			tlsOptions = &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}
		}
		if err := client.StartTLS(tlsOptions); err != nil {
			return err
		}
	}
	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.from); err != nil {
		return err
	}
	if err := client.Rcpt(msg.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	boundary := "gogogadget-boundary"
	body := "From: " + s.from + "\r\n" +
		"To: " + msg.To + "\r\n" +
		"Subject: " + msg.Subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + msg.Text + "\r\n" +
		"--" + boundary + "\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" + msg.HTML + "\r\n" +
		"--" + boundary + "--\r\n"
	if _, err := writer.Write([]byte(body)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

var _ mail.Sender = (*SMTPSender)(nil)
