package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Phydran6/LocoStor/internal/raid"
	"github.com/Phydran6/LocoStor/internal/smart"
	"github.com/Phydran6/LocoStor/internal/sysinfo"
)

func waitHistory(t *testing.T, m *Manager, n int) []Message {
	t.Helper()
	for i := 0; i < 200; i++ {
		if h := m.History(); len(h) >= n {
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("want %d messages, got %+v", n, m.History())
	return nil
}

func TestMonitor(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "n.json"), filepath.Join(dir, "h.json"), "nas")
	health := "ok"
	temp := 40
	mo := &Monitor{N: m,
		RAID: func() ([]raid.Array, error) {
			return []raid.Array{{Name: "md0", Level: "raid1", Health: health, RaidDisks: 2, ActiveDisks: 1, Status: "U_", Progress: -1}}, nil
		},
		SMART: func(context.Context) ([]smart.Disk, error) {
			return []smart.Disk{{Device: "/dev/sda", Serial: "S1", Health: "ok", Temperature: &temp}}, nil
		},
		FS: func() []sysinfo.Filesystem {
			return []sysinfo.Filesystem{{Mount: "/mnt/raid", Size: 100, Used: 95, Avail: 5}}
		},
	}
	mo.CheckRAID() // healthy at start: nothing
	health = "degraded"
	mo.CheckRAID()
	mo.CheckRAID() // no repeat
	h := waitHistory(t, m, 1)
	if !strings.Contains(h[0].Title, "DEGRADED") || h[0].Severity != Error {
		t.Errorf("degraded alert: %+v", h[0])
	}
	temp = 60
	mo.CheckSMART(context.Background())
	mo.CheckSpace()
	h = waitHistory(t, m, 3)
	all := fmt.Sprint(h)
	if !strings.Contains(all, "hot: 60") || !strings.Contains(all, "95% full") {
		t.Errorf("missing alerts: %s", all)
	}
}

func TestDetectAndMessage(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "n.json"), "", "nas")
	for email, want := range map[string]string{"a@gmail.com": "google", "b@web.de": "webde", "c@outlook.de": "microsoft", "d@gmx.net": "gmx", "e@t-online.de": "tonline"} {
		p, err := m.Detect(context.Background(), email)
		if err != nil || p.ID != want {
			t.Errorf("%s: %s %v", email, p.ID, err)
		}
	}
	msg := string(buildMessage("a@b.de", []string{"x@y.de"}, "RAID md0 ist DEGRADED – Ärger", "Zeile 1\nZeile 2 äöü", "smtp.b.de"))
	if !strings.Contains(msg, "Subject: =?utf-8?q?") || !strings.Contains(msg, "Zeile 1\r\nZeile 2") {
		t.Errorf("message:\n%s", msg)
	}
}

func TestTelegramFlow(t *testing.T) {
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"username":"nas_alert_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			fmt.Fprintf(w, `{"ok":true,"result":[{"update_id":5,"message":{"text":"/start %s","chat":{"id":42,"type":"private","first_name":"Ann","username":"ann"}}},{"update_id":6,"message":{"text":"hi","chat":{"id":7}}}]}`, code)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			sent = append(sent, r.Form.Get("chat_id")+":"+r.Form.Get("text"))
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
		default:
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		}
	}))
	defer srv.Close()
	TelegramAPI = srv.URL
	m := New(filepath.Join(t.TempDir(), "n.json"), "", "nas")
	if _, err := m.StartTelegram(context.Background(), "not-a-token"); err == nil {
		t.Error("bad token accepted")
	}
	link, err := m.StartTelegram(context.Background(), "123456789:AAHabcdefghijklmnopqrstuvwxyz0123456")
	if err != nil {
		t.Fatal(err)
	}
	code = link.Code
	if !strings.HasPrefix(link.Link, "https://t.me/nas_alert_bot?start=locostor-") {
		t.Errorf("link %s", link.Link)
	}
	chats, err := m.TelegramChats(context.Background())
	if err != nil || len(chats) != 1 || chats[0].ID != 42 || !strings.Contains(chats[0].Title, "@ann") {
		t.Fatalf("chats %+v %v", chats, err)
	}
	if err := m.SaveTelegram(true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(context.Background(), "test", Warning, "Test <b>", "body & more"); err != nil {
		t.Fatal(err)
	}
	last := sent[len(sent)-1]
	if !strings.HasPrefix(last, "42:") || !strings.Contains(last, "Test &lt;b&gt;") || !strings.Contains(last, "body &amp; more") {
		t.Errorf("sent %q", sent)
	}
	if p := m.Public(); p.Telegram == nil || p.Telegram.Bot != "nas_alert_bot" || len(p.Telegram.Chats) != 1 {
		t.Errorf("public %+v", p.Telegram)
	}
}

var code string
