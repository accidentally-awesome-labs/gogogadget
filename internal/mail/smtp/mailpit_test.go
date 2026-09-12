package smtp

// The protocol-container tier for this adapter: the SMTP client this package
// ships, run against a real mail server.
//
// contract_test.go beside this file runs the SAME contract against fakeSMTP,
// a ~35-line in-process server written in this package. That fake answers
// `250 ok` to almost everything and hands the DATA block back as a string, so
// it can prove the adapter speaks a sequence this package already believed in
// — and nothing more. It never parses the message: a MIME body with a
// mangled boundary, a lost part, or a header the server would reject reads
// identically through it, because the assertion is `strings.Contains` over
// the bytes we just wrote.
//
// Mailpit is a real SMTP server, and it is already this adapter's declared
// `mailpit` service target — digest-pinned at
// registry/modules/system/mail-smtp/module.json with a local_service block
// publishing SMTP on 1025 and an HTTP API on 8025 — and until this file
// nothing in the repository had ever run against it. The HTTP API is what
// makes the container tier worth its cost here: it returns the message as the
// SERVER parsed it, so this file asserts the envelope and both MIME parts as
// received rather than as sent.
//
// The fake-backed run stays. This ADDS a real-server run beside it; the fake
// is what keeps the command sequence assertable with no container at all.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
	mailcontract "github.com/gogogadget/gogogadget/internal/mail/contract"
	"github.com/stretchr/testify/require"
)

// mailpitDefaultAPIPort is the HTTP read-back port this adapter's `mailpit`
// target declares (the local_service `web` port in
// registry/modules/system/mail-smtp/module.json). It is the default rather
// than a requirement: MAILPIT_API_URL overrides it, because a contributor
// whose 8025 is already taken remaps the published port and should not have
// to free it to run this tier.
const mailpitDefaultAPIPort = 8025

func TestSMTPSenderAgainstMailpit(t *testing.T) {
	// SMTP_HOST is this adapter's own declared host key
	// (registry/modules/system/mail-smtp/module.json). It carries a DECLARED
	// DEFAULT of localhost, which is why the test reads the process
	// environment directly: a declared default is the address this adapter
	// WOULD use, equally true whether or not anything listens there, while an
	// exported SMTP_HOST is somebody naming a server. That is the same
	// distinction internal/db/testdb/testdb.go:103-138 draws between a
	// derived and a named address — see requireReachableSMTP below.
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		t.Skipf("[inapplicable] the Mailpit run needs a real SMTP server plus its HTTP read-back API and SMTP_HOST names none " +
			"(measured 0.02 s against a warm container, plus the image pull on a cold one); CI's `test` job owns it and " +
			"sets it beside the mailpit service container — to run it here, start the `mailpit` service target this adapter " +
			"declares (the local_service block in registry/modules/system/mail-smtp/module.json) and export " +
			"SMTP_HOST=127.0.0.1 SMTP_PORT=1025")
	}
	port := requireNamedPort(t, host)
	requireReachableSMTP(t, host, port)
	api := mailpitAPIBase(t, host)

	// Read-back is only a claim about THIS run if the mailbox starts empty.
	// Mailpit keeps everything it has ever received, so without this the
	// assertions below could be satisfied by a message an earlier run sent.
	mailpitRequest(t, http.MethodDelete, api+"/api/v1/messages")

	cfg := &config.Config{
		Values:    map[string]string{"SMTP_HOST": host, "SMTP_PORT": strconv.Itoa(port)},
		EmailFrom: "canary@gogogadget.test",
	}
	module, err := NewModule(context.Background(), apphost.Map(nil, time.Now(), "test"), Deps{Config: cfg})
	require.NoError(t, err)

	// The same contract the fake-backed run executes, unchanged: one send
	// that must succeed against a real server, and one cancelled context that
	// must not reach the wire.
	mailcontract.Run(t, func() mail.Sender { return module.Sender })

	// mailcontract.Run sends exactly one deliverable message. Anything else
	// in the mailbox means the cancelled-context case reached the server,
	// which is the contract's other half read from the server's side.
	messages := mailpitMessages(t, api)
	require.Len(t, messages, 1, "one deliverable send must leave one message; a cancelled Send must leave none")

	full := mailpitMessage(t, api, messages[0].ID)

	t.Run("the envelope arrived as the adapter addressed it", func(t *testing.T) {
		// Parsed by the server, not by us. The fake read these out of the
		// string it had just been handed, so a From header the adapter wrote
		// into the wrong position, or a To the server would have rewritten,
		// was invisible to it.
		require.Equal(t, cfg.EmailFrom, full.From.Address)
		require.Len(t, full.To, 1)
		require.Equal(t, "contract@example.com", full.To[0].Address)
		require.Equal(t, "Contract subject", full.Subject)
	})

	t.Run("both MIME parts arrived with the bytes the renderer produced", func(t *testing.T) {
		// The claim the fake cannot make. Send writes a multipart/alternative
		// body by hand (smtp.go:93-101); a boundary typo, a missing blank
		// line after a part header, or a dropped closing delimiter all still
		// contain both substrings, so the fake-backed assertion passes over a
		// message no client could render. These are the parts Mailpit's own
		// MIME parser extracted.
		//
		// The compare trims trailing whitespace because the CRLF before each
		// boundary delimiter belongs to the MIME framing, not to the part —
		// RFC 2046 5.1.1 — so the server is right to keep or drop it and this
		// test must not pin either choice.
		require.Equal(t, "<p>Contract body</p>", strings.TrimRight(full.HTML, "\r\n"),
			"the html part must arrive as the renderer produced it")
		require.Equal(t, "Contract body", strings.TrimRight(full.Text, "\r\n"),
			"the text part must arrive as the renderer produced it")
		require.NotEmpty(t, full.HTML, "a multipart/alternative message with no html part is a broken body")
		require.NotEmpty(t, full.Text, "a multipart/alternative message with no text part is a broken body")
	})
}

// mailpitMessageSummary and mailpitFullMessage are the slices of Mailpit's
// HTTP API this test reads: the listing, which supplies the id, and one
// message as the server parsed it. Declared rather than decoded into
// map[string]any so a renamed field is a zero-value failure that names itself
// instead of a missing map key nobody looked for.
type mailpitAddress struct {
	Address string `json:"Address"`
}

type mailpitMessageSummary struct {
	ID string `json:"ID"`
}

type mailpitFullMessage struct {
	Subject string           `json:"Subject"`
	From    mailpitAddress   `json:"From"`
	To      []mailpitAddress `json:"To"`
	HTML    string           `json:"HTML"`
	Text    string           `json:"Text"`
}

func mailpitMessages(t *testing.T, api string) []mailpitMessageSummary {
	t.Helper()
	var listing struct {
		Messages []mailpitMessageSummary `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(mailpitRequest(t, http.MethodGet, api+"/api/v1/messages"), &listing))
	return listing.Messages
}

func mailpitMessage(t *testing.T, api, id string) mailpitFullMessage {
	t.Helper()
	var message mailpitFullMessage
	require.NoError(t, json.Unmarshal(mailpitRequest(t, http.MethodGet, api+"/api/v1/message/"+id), &message))
	return message
}

// mailpitRequest performs one API call and fails on anything but 2xx. A
// read-back that silently tolerated a 404 would turn every assertion below it
// into a comparison against the zero value.
func mailpitRequest(t *testing.T, method, target string) []byte {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	require.NoError(t, err, "Mailpit's HTTP API did not answer at %s; set MAILPIT_API_URL if the web port is remapped", target)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Less(t, resp.StatusCode, 300, "%s %s returned %d: %s", method, target, resp.StatusCode, body)
	return body
}

// mailpitAPIBase resolves the read-back endpoint: MAILPIT_API_URL when the
// published web port has been remapped, otherwise the port this adapter's
// mailpit target declares beside the SMTP one.
func mailpitAPIBase(t *testing.T, host string) string {
	t.Helper()
	if override := os.Getenv("MAILPIT_API_URL"); override != "" {
		parsed, err := url.Parse(override)
		require.NoError(t, err, "MAILPIT_API_URL=%q is not a URL", override)
		require.NotEmpty(t, parsed.Host, "MAILPIT_API_URL=%q has no host", override)
		return strings.TrimSuffix(override, "/")
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(mailpitDefaultAPIPort))
}

// requireNamedPort reads SMTP_PORT, failing rather than skipping when it is
// absent. SMTP_HOST was exported, so somebody named a server, and an address
// is a host AND a port: falling back to the declared 1025 for a host that was
// named explicitly would silently probe a different server than the one asked
// for.
func requireNamedPort(t *testing.T, host string) int {
	t.Helper()
	raw := os.Getenv("SMTP_PORT")
	if raw == "" {
		t.Fatalf("SMTP_PORT is unset while SMTP_HOST names %s: an address is a host and a port, so a named host with "+
			"no port is an incomplete configuration and not an absent server. Export SMTP_PORT, or unset SMTP_HOST to "+
			"let this tier skip.", host)
	}
	port, err := strconv.Atoi(raw)
	require.NoError(t, err, "SMTP_PORT=%q is not a number", raw)
	return port
}

// requireReachableSMTP mirrors internal/db/testdb/testdb.go:103-138, and the
// rule is that function's rule with the address swapped.
//
// SMTP_HOST being SET is a request: somebody exported an address for this
// suite, and CI's `test` job does exactly that beside the service container
// that answers on it. The manifest's DECLARED DEFAULT of localhost is not a
// request — it is the address this adapter would use, equally true whether or
// not anything listens there. So a named server that does not answer is a
// FAILURE and an absent variable is an ABSENCE, which is the skip above.
// Before testdb drew that split, every unreachable server was an absence: the
// stack was torn down between rounds, an entire package's integration tests
// skipped, `go test` printed `ok`, and that was reported as a pass.
func requireReachableSMTP(t *testing.T, host string, port int) {
	t.Helper()
	address := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("SMTP server unreachable at %s: %v\n"+
			"SMTP_HOST names this server, so one that does not answer is a broken mail server and not an absent one. "+
			"Start the `mailpit` service target this adapter declares (the local_service block in "+
			"registry/modules/system/mail-smtp/module.json), or unset SMTP_HOST to let this tier skip.", address, err)
		return
	}
	_ = conn.Close()
}
