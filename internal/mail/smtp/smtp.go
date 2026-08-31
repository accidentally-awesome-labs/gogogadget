// Package smtp implements the self-hosted SMTP mail adapter.
package smtp

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
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
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		host = "localhost"
	}
	port := 1025
	if raw := os.Getenv("SMTP_PORT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			port = parsed
		}
	}
	return &Module{Sender: NewSMTPSender(host, port, os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD"), d.Config.EmailFrom)}, nil
}

type SMTPSender struct {
	host, username, password, from string
	port                           int
}

func NewSMTPSender(host string, port int, username, password, from string) *SMTPSender {
	return &SMTPSender{host: host, port: port, username: username, password: password, from: from}
}

func (s *SMTPSender) Send(ctx context.Context, msg mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	address := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()
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
	body := "From: " + s.from + "\r\nTo: " + msg.To + "\r\nSubject: " + msg.Subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" + msg.HTML + "\r\n"
	if _, err := writer.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

var _ mail.Sender = (*SMTPSender)(nil)
