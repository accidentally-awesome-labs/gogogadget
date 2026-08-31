package smtp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
	mailcontract "github.com/gogogadget/gogogadget/internal/mail/contract"
	"github.com/stretchr/testify/require"
)

func TestSMTPModuleContract(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	payload := make(chan string, 1)
	go fakeSMTP(t, ln, payload)
	port := ln.Addr().(*net.TCPAddr).Port
	host := apphost.Map(nil, time.Now(), "test")
	cfg := &config.Config{Values: map[string]string{"SMTP_HOST": "127.0.0.1", "SMTP_PORT": fmt.Sprint(port)}, EmailFrom: "hello@example.com"}
	module, err := NewModule(context.Background(), host, Deps{Config: cfg})
	require.NoError(t, err)
	mailcontract.Run(t, func() mail.Sender { return module.Sender })
	body := <-payload
	require.Contains(t, body, "Contract body")
	require.Contains(t, body, "<p>Contract body</p>")
	require.NotContains(t, body, "\r\r\n")
}

func TestSMTPModuleUsesTypedDefaultsAndRejectsInvalidPort(t *testing.T) {
	module, err := NewModule(context.Background(), apphost.Map(nil, time.Now(), "test"), Deps{Config: &config.Config{Values: map[string]string{}}})
	require.NoError(t, err)
	s := module.Sender.(*SMTPSender)
	require.Equal(t, "localhost", s.host)
	require.Equal(t, 1025, s.port)
	_, err = NewModule(context.Background(), apphost.Map(nil, time.Now(), "test"), Deps{Config: &config.Config{Values: map[string]string{"SMTP_PORT": "bad"}}})
	require.Error(t, err)
}

func fakeSMTP(t *testing.T, ln net.Listener, payload chan<- string) {
	t.Helper()
	conn, err := ln.Accept()
	require.NoError(t, err)
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "220 fake\r\n")
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err)
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			_, _ = fmt.Fprint(conn, "250-fake\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(command, "MAIL"), strings.HasPrefix(command, "RCPT"):
			_, _ = fmt.Fprint(conn, "250 ok\r\n")
		case command == "DATA":
			_, _ = fmt.Fprint(conn, "354 send\r\n")
			var lines []string
			for {
				line, err = r.ReadString('\n')
				require.NoError(t, err)
				if line == ".\r\n" {
					break
				}
				lines = append(lines, line)
			}
			payload <- strings.Join(lines, "")
			_, _ = fmt.Fprint(conn, "250 queued\r\n")
		case command == "QUIT":
			_, _ = fmt.Fprint(conn, "221 bye\r\n")
			return
		default:
			_, _ = fmt.Fprint(conn, "250 ok\r\n")
		}
	}
}

func TestSMTPModuleRejectsMissingConfig(t *testing.T) {
	_, err := NewModule(context.Background(), apphost.Map(nil, time.Now(), "test"), Deps{})
	require.Error(t, err)
}
