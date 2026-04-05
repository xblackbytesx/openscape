package auth

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
)

const (
	sessionName    = "openscape_session"
	keyUserID      = "user_id"
	keyFlashError  = "flash_error"
	keyFlashSuccess = "flash_success"
)

var store *sessions.CookieStore

func InitStore(secret string, secure bool) {
	store = sessions.NewCookieStore([]byte(secret))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30, // 30 days
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func SetUserID(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		sess, _ = store.New(r, sessionName)
	}
	sess.Values[keyUserID] = userID.String()
	return store.Save(r, w, sess)
}

func GetUserID(r *http.Request) (uuid.UUID, bool) {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		return uuid.Nil, false
	}
	raw, ok := sess.Values[keyUserID]
	if !ok {
		return uuid.Nil, false
	}
	s, ok := raw.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func ClearSession(w http.ResponseWriter, r *http.Request) error {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		return err
	}
	sess.Options.MaxAge = -1
	return store.Save(r, w, sess)
}

func SetFlashError(w http.ResponseWriter, r *http.Request, msg string) error {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		sess, _ = store.New(r, sessionName)
	}
	sess.AddFlash(msg, keyFlashError)
	return store.Save(r, w, sess)
}

func SetFlashSuccess(w http.ResponseWriter, r *http.Request, msg string) error {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		sess, _ = store.New(r, sessionName)
	}
	sess.AddFlash(msg, keyFlashSuccess)
	return store.Save(r, w, sess)
}

func GetFlashes(w http.ResponseWriter, r *http.Request) (errors []string, successes []string) {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		return nil, nil
	}
	for _, f := range sess.Flashes(keyFlashError) {
		if s, ok := f.(string); ok {
			errors = append(errors, s)
		}
	}
	for _, f := range sess.Flashes(keyFlashSuccess) {
		if s, ok := f.(string); ok {
			successes = append(successes, s)
		}
	}
	_ = store.Save(r, w, sess)
	return errors, successes
}
