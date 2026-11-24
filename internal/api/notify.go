package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Phydran6/LocoStor/internal/notify"
)

func (s *Server) notifyRoutes(api *http.ServeMux) {
	n := s.Notify
	if n == nil {
		return
	}
	api.HandleFunc("GET /api/notify", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"config": n.Public(), "providers": notify.Providers})
	})
	api.HandleFunc("PUT /api/notify/events", func(w http.ResponseWriter, r *http.Request) {
		var body notify.Events
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		respond(w, n.SetEvents(body))
	})
	api.HandleFunc("POST /api/notify/test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		msg, err := n.Send(ctx, "test", notify.Info, "Test message", "If you can read this, LocoStor notifications work.")
		writeJSON(w, http.StatusOK, map[string]any{"message": msg, "error": errString(err)})
	})

	// E-mail
	api.HandleFunc("POST /api/notify/email/detect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email string `json:"email"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		p, err := n.Detect(ctx, body.Email)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	api.HandleFunc("PUT /api/notify/email", func(w http.ResponseWriter, r *http.Request) {
		var body notify.EmailConfig
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		respond(w, n.SetEmail(body))
	})
	api.HandleFunc("DELETE /api/notify/email", func(w http.ResponseWriter, r *http.Request) {
		respond(w, n.DeleteEmail())
	})
	api.HandleFunc("POST /api/notify/email/oauth/start", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Provider     string `json:"provider"`
			Email        string `json:"email"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			Tenant       string `json:"tenant"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		st, err := n.StartOAuth(ctx, body.Provider, body.Email, body.ClientID, body.ClientSecret, body.Tenant)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	api.HandleFunc("GET /api/notify/email/oauth/status", func(w http.ResponseWriter, r *http.Request) {
		st, msg := n.OAuthStatus()
		writeJSON(w, http.StatusOK, map[string]string{"status": st, "error": msg})
	})
	api.HandleFunc("POST /api/notify/email/oauth/code", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URL string `json:"url"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		respond(w, n.FinishGoogle(ctx, body.URL))
	})

	// Telegram
	api.HandleFunc("POST /api/notify/telegram/token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		link, err := n.StartTelegram(ctx, body.Token)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, link)
	})
	api.HandleFunc("GET /api/notify/telegram/chats", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		chats, err := n.TelegramChats(ctx)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, chats)
	})
	api.HandleFunc("PUT /api/notify/telegram", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool    `json:"enabled"`
			Keep    []int64 `json:"keep"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		respond(w, n.SaveTelegram(body.Enabled, body.Keep))
	})
	api.HandleFunc("DELETE /api/notify/telegram", func(w http.ResponseWriter, r *http.Request) {
		respond(w, n.DeleteTelegram())
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
