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
	module, err := NewModule(context.Background(), host, Deps{Config: &config.Config{EmailFrom: "hello@example.com", SMTPHost: "127.0.0.1", SMTPPort: port}})
	require.NoError(t, err)
	require.NoError(t, module.Sender.Send(context.Background(), mail.Message{To: "to@example.com", Subject: "subject", Text: "plain", HTML: "<b>html</b>"}))
	body := <-payload
	require.Contains(t, body, "plain")
	require.Contains(t, body, "<b>html</b>")
	require.NotContains(t, body, "\r\r\n")
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
