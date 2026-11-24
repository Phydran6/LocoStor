package notify

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSMTP accepts one plain-text session with AUTH LOGIN.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { c.Write([]byte(s + "\r\n")) }
		var log strings.Builder
		w("220 fake ESMTP")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					w("250 queued")
					continue
				}
				log.WriteString(line + "\n")
				continue
			}
			cmd := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				w("250-fake\r\n250 AUTH LOGIN")
			case cmd == "AUTH LOGIN":
				w("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				u, _ := r.ReadString('\n')
				w("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				p, _ := r.ReadString('\n')
				ud, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
				pd, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
				log.WriteString("auth " + string(ud) + "/" + string(pd) + "\n")
				w("235 ok")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				log.WriteString(line + "\n")
				w("250 ok")
			case cmd == "DATA":
				inData = true
				w("354 go")
			case cmd == "QUIT":
				w("221 bye")
				got <- log.String()
				return
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSendMail(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	m := New(filepath.Join(t.TempDir(), "n.json"), "", "nas")
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	err := m.SetEmail(EmailConfig{Enabled: true, Provider: "custom", Host: host, Port: p, Security: "none", Username: "me@web.de", Password: "pw", From: "me@web.de", To: []string{"you@web.de"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(context.Background(), "test", Info, "Hallo", "Body"); err != nil {
		t.Fatal(err)
	}
	log := <-got
	for _, want := range []string{"auth me@web.de/pw", "MAIL FROM:<me@web.de>", "RCPT TO:<you@web.de>", "Subject: [nas] Hallo", "Body"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in\n%s", want, log)
		}
	}
	if pub := m.Public(); pub.Email == nil || !pub.Email.HasPassword {
		t.Errorf("public: %+v", pub.Email)
	}
}
