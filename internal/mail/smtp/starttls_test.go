package smtp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/stretchr/testify/require"
)

func TestSMTPSenderSTARTTLSAndAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "smtp.test"}, DNSNames: []string{"smtp.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots.AddCert(parsed)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	seenAuth := make(chan bool, 1)
	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		fmt.Fprint(conn, "220 fake\r\n")
		r := bufio.NewReader(conn)
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				return
			}
			cmd := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				fmt.Fprint(conn, "250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
			case cmd == "STARTTLS":
				fmt.Fprint(conn, "220 go\r\n")
				tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
				if tc.Handshake() != nil {
					return
				}
				conn = tc
				r = bufio.NewReader(conn)
			case strings.HasPrefix(cmd, "AUTH PLAIN"):
				seenAuth <- true
				fmt.Fprint(conn, "235 ok\r\n")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				fmt.Fprint(conn, "250 ok\r\n")
			case cmd == "DATA":
				fmt.Fprint(conn, "354 go\r\n")
				for {
					line, e = r.ReadString('\n')
					if e != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
				}
				fmt.Fprint(conn, "250 queued\r\n")
			case cmd == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				return
			}
		}
	}()
	s := NewSMTPSender("smtp.test", ln.Addr().(*net.TCPAddr).Port, "user", "pass", "from@example.com")
	s.tlsConfig = &tls.Config{ServerName: "smtp.test", RootCAs: roots, MinVersion: tls.VersionTLS12}
	s.dialAddr = "127.0.0.1"
	require.NoError(t, s.Send(context.Background(), mail.Message{To: "to@example.com", Subject: "s", Text: "t", HTML: "<b>h</b>"}))
	require.True(t, <-seenAuth)
}
