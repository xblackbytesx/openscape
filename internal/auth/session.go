package auth

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
)

const (
	sessionName = "openscape_session"
	keyUserID   = "user_id"
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

