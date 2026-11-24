package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Phydran6/LocoStor/internal/valid"
)

// TelegramConfig is a bot created with @BotFather and the chats it
// reports to.
type TelegramConfig struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
	Bot     string `json:"bot"` // username without @
	Chats   []Chat `json:"chats"`
}

// Chat is a Telegram chat (person or group) that receives messages.
type Chat struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// TelegramPublic is TelegramConfig without the token.
type TelegramPublic struct {
	Enabled bool   `json:"enabled"`
	Bot     string `json:"bot"`
	Chats   []Chat `json:"chats"`
}

func (t TelegramConfig) public() TelegramPublic {
	return TelegramPublic{Enabled: t.Enabled, Bot: t.Bot, Chats: append([]Chat{}, t.Chats...)}
}

type pendingTelegram struct {
	token  string
	bot    string
	code   string
	offset int64
	chats  []Chat
}

var tgTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)

// TelegramAPI is the Bot API base URL (tests replace it).
var TelegramAPI = "https://api.telegram.org"

func (m *Manager) tgCall(ctx context.Context, token, method string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TelegramAPI+"/bot"+token+"/"+method, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.client.Do(req)
	if err != nil {
		// The URL contains the token - never show it.
		return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("telegram: %s", resp.Status)
	}
	if !r.OK {
		return fmt.Errorf("telegram: %s", r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// TelegramLink is what the user opens to connect a chat.
type TelegramLink struct {
	Bot  string `json:"bot"`
	Link string `json:"link"`
	Code string `json:"code"`
}

// StartTelegram checks a bot token from @BotFather and returns a link that
// connects the user's chat with one tap.
func (m *Manager) StartTelegram(ctx context.Context, token string) (TelegramLink, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		m.mu.Lock()
		if m.cfg.Telegram != nil {
			token = m.cfg.Telegram.Token
		}
		m.mu.Unlock()
	}
	if !tgTokenRe.MatchString(token) {
		return TelegramLink{}, valid.Errorf("this is not a bot token - it looks like 123456789:AAH...")
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := m.tgCall(ctx, token, "getMe", nil, &me); err != nil {
		return TelegramLink{}, valid.Errorf("the token does not work: %v", err)
	}
	// A webhook would swallow the updates we wait for.
	_ = m.tgCall(ctx, token, "deleteWebhook", nil, nil)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	code := "locostor-" + hex.EncodeToString(b)
	p := &pendingTelegram{token: token, bot: me.Username, code: code}
	m.mu.Lock()
	m.tgPend = p
	m.mu.Unlock()
	return TelegramLink{Bot: me.Username, Code: code, Link: "https://t.me/" + me.Username + "?start=" + code}, nil
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID        int64  `json:"id"`
			Type      string `json:"type"`
			Title     string `json:"title"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"chat"`
	} `json:"message"`
}

// TelegramChats returns the chats that sent the start code so far.
func (m *Manager) TelegramChats(ctx context.Context) ([]Chat, error) {
	m.mu.Lock()
	p := m.tgPend
	m.mu.Unlock()
	if p == nil {
		return nil, valid.Errorf("enter the bot token first")
	}
	var ups []tgUpdate
	params := url.Values{"timeout": {"0"}, "allowed_updates": {`["message"]`}}
	if p.offset > 0 {
		params.Set("offset", strconv.FormatInt(p.offset, 10))
	}
	if err := m.tgCall(ctx, p.token, "getUpdates", params, &ups); err != nil {
		return nil, err
	}
	for _, u := range ups {
		if u.UpdateID >= p.offset {
			p.offset = u.UpdateID + 1
		}
		if u.Message == nil || !strings.Contains(u.Message.Text, p.code) {
			continue
		}
		c := u.Message.Chat
		title := c.Title
		if title == "" {
			title = strings.TrimSpace(c.FirstName + " " + c.LastName)
			if c.Username != "" {
				title += " (@" + c.Username + ")"
			}
		}
		dup := false
		for _, x := range p.chats {
			dup = dup || x.ID == c.ID
		}
		if !dup {
			p.chats = append(p.chats, Chat{ID: c.ID, Title: title})
			_ = m.tgCall(ctx, p.token, "sendMessage", url.Values{"chat_id": {strconv.FormatInt(c.ID, 10)},
				"text": {"✅ Connected with LocoStor on " + m.Hostname + ". You will get alerts here."}}, nil)
		}
	}
	return append([]Chat{}, p.chats...), nil
}

// SaveTelegram stores the bot with the connected chats (and keeps chats
// that were already set up).
func (m *Manager) SaveTelegram(enabled bool, keep []int64) error {
	m.mu.Lock()
	p := m.tgPend
	m.mu.Unlock()
	return m.update(func(c *Config) error {
		t := TelegramConfig{Enabled: enabled}
		if c.Telegram != nil {
			t = *c.Telegram
			t.Enabled = enabled
		}
		if p != nil {
			if p.token != t.Token {
				t.Chats = nil
			}
			t.Token, t.Bot = p.token, p.bot
		}
		if t.Token == "" {
			return valid.Errorf("connect a bot first")
		}
		chats := []Chat{}
		keepSet := map[int64]bool{}
		for _, id := range keep {
			keepSet[id] = true
		}
		for _, ch := range t.Chats {
			if keep == nil || keepSet[ch.ID] {
				chats = append(chats, ch)
			}
		}
		if p != nil {
			for _, ch := range p.chats {
				dup := false
				for _, x := range chats {
					dup = dup || x.ID == ch.ID
				}
				if !dup {
					chats = append(chats, ch)
				}
			}
		}
		if len(chats) == 0 && enabled {
			return valid.Errorf("no chat is connected yet - open the link and press Start")
		}
		t.Chats = chats
		c.Telegram = &t
		return nil
	})
}

// DeleteTelegram removes the bot.
func (m *Manager) DeleteTelegram() error {
	m.mu.Lock()
	m.tgPend = nil
	m.mu.Unlock()
	return m.update(func(c *Config) error { c.Telegram = nil; return nil })
}

var tgIcons = map[string]string{Info: "ℹ️", Warning: "⚠️", Error: "🚨"}

func (m *Manager) sendTelegram(ctx context.Context, t TelegramConfig, severity, title, body string) error {
	text := tgIcons[severity] + " <b>" + html.EscapeString(title) + "</b>\n\n" + html.EscapeString(body)
	if len(text) > 4000 {
		text = text[:4000] + "…"
	}
	var errs []string
	for _, c := range t.Chats {
		err := m.tgCall(ctx, t.Token, "sendMessage", url.Values{
			"chat_id": {strconv.FormatInt(c.ID, 10)}, "text": {text}, "parse_mode": {"HTML"}, "disable_web_page_preview": {"true"},
		}, nil)
		if err != nil {
			errs = append(errs, c.Title+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
