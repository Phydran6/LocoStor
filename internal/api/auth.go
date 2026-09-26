package api

import (
	"errors"
	"net/http"

	"github.com/Phydran6/LocoStor/internal/auth"
	"github.com/Phydran6/LocoStor/internal/valid"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	res, err := s.Auth.Login(auth.ClientIP(r), body.Username, body.Password)
	if err != nil {
		authError(w, err)
		return
	}
	if res.MFAToken != "" {
		writeJSON(w, http.StatusOK, map[string]any{"mfa_required": true, "mfa_token": res.MFAToken})
		return
	}
	auth.SetCookie(w, r, res.Token)
	s.writeMe(w, true)
}

func (s *Server) loginMFA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	token, err := s.Auth.VerifyMFA(auth.ClientIP(r), body.MFAToken, body.Code)
	if err != nil {
		authError(w, err)
		return
	}
	auth.SetCookie(w, r, token)
	s.writeMe(w, true)
}

func authError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrThrottled) {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	writeError(w, http.StatusUnauthorized, err.Error())
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(auth.Token(r))
	auth.ClearCookie(w, r)
	ok(w)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	s.writeMe(w, s.Auth.Valid(auth.Token(r)))
}

// writeMe answers without a session too (the login page needs to know the
// mode), so it reveals nothing beyond the version.
func (s *Server) writeMe(w http.ResponseWriter, loggedIn bool) {
	writeJSON(w, http.StatusOK, map[string]any{
		"logged_in": loggedIn,
		"version":   s.Version,
		"demo":      s.Demo,
	})
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Auth.Info())
}

// accountError maps auth errors to user-facing validation errors.
func accountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalid):
		fail(w, valid.Errorf("the password is wrong"))
	case errors.Is(err, auth.ErrMFA):
		fail(w, valid.Errorf("the code is wrong"))
	default:
		fail(w, valid.Errorf("%s", err.Error()))
	}
}

func (s *Server) denyInDemo(w http.ResponseWriter) bool {
	if s.Demo {
		fail(w, valid.Errorf("not available in demo mode"))
	}
	return s.Demo
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if s.denyInDemo(w) {
		return
	}
	if err := s.Auth.ChangePassword(auth.Token(r), body.Current, body.New); err != nil {
		accountError(w, err)
		return
	}
	ok(w)
}

func (s *Server) changeUsername(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Username string `json:"username"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if s.denyInDemo(w) {
		return
	}
	if err := s.Auth.ChangeUsername(body.Password, body.Username); err != nil {
		accountError(w, err)
		return
	}
	ok(w)
}

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	secret, uri := s.Auth.BeginTOTP()
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	codes, err := s.Auth.EnableTOTP(auth.Token(r), body.Code)
	if err != nil {
		accountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if err := s.Auth.DisableTOTP(body.Password, body.Code); err != nil {
		accountError(w, err)
		return
	}
	ok(w)
}

func (s *Server) recoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	codes, err := s.Auth.RegenerateRecovery(body.Password, body.Code)
	if err != nil {
		accountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}
