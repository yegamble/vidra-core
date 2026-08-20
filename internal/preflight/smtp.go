package preflight

import (
	"bufio"
	"context"
	"net"
	"strings"
)

// CheckSMTP dials addr (host:port) and reads the relay's greeting line, then
// hangs up. No EHLO, no AUTH, no message: the question is "is there a relay
// there and does it talk to us", and anything more would be sending mail from a
// diagnostic — a check that logs into the relay on every run is a check that
// eventually gets the account rate-limited.
//
// A healthy relay opens with a line beginning "220"; the CALLER decides what a
// different greeting means, because a proxy or captive portal answering here is
// a warning in `vidra doctor` and a down component on the admin status page.
// The dial and the read are both bounded by ctx.
func CheckSMTP(ctx context.Context, addr string) (banner string, err error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
