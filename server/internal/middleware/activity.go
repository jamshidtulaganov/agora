package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// activityWindow is how often a user's last_active_at may be rewritten. The
// admin page asks "who used Agora this week", so minutes of precision are
// plenty, and one UPDATE per user per window keeps the write cost trivial.
const activityWindow = 10 * time.Minute

// ActivityToucher is the one query TouchUserActivity needs.
type ActivityToucher interface {
	TouchUserLastActive(ctx context.Context, id pgtype.UUID) error
}

// TouchUserActivity records that a human used the app. It runs after Auth,
// skips machine actors (agent task tokens, cloud PATs — anything with a
// server-set X-Actor-Source), and never delays or fails the request.
func TouchUserActivity(q ActivityToucher) func(http.Handler) http.Handler {
	var last sync.Map // user id string -> time.Time
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get("X-User-ID")
			if userID != "" && r.Header.Get("X-Actor-Source") == "" {
				now := time.Now()
				prev, seen := last.Load(userID)
				if !seen || now.Sub(prev.(time.Time)) >= activityWindow {
					last.Store(userID, now)
					var id pgtype.UUID
					if err := id.Scan(userID); err == nil {
						go func() {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							if err := q.TouchUserLastActive(ctx, id); err != nil {
								slog.Debug("activity: stamp failed", "error", err)
							}
						}()
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
