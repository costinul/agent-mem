package auth

import (
	"context"
	"net/http"

	models "agentmem/internal/models"
	"agentmem/internal/repository/userrepo"
)

type ctxKey string

const userContextKey ctxKey = "auth_user"

func RequireAdmin(sessionStore SessionStore, userRepo userrepo.Repository, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/admin/login", http.StatusTemporaryRedirect)
			return
		}

		sess, err := sessionStore.Validate(r.Context(), cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusTemporaryRedirect)
			return
		}

		user, err := userRepo.GetByID(r.Context(), sess.UserID)
		if err != nil || user == nil {
			http.Redirect(w, r, "/admin/login", http.StatusTemporaryRedirect)
			return
		}

		if user.Role != "admin" {
			http.Error(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserFromContext(ctx context.Context) *models.User {
	u, _ := ctx.Value(userContextKey).(*models.User)
	return u
}
