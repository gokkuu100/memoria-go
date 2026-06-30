// Package server builds the HTTP router: global middleware, health probes,
// and the versioned /v1 API mount that feature modules attach to.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/albums"
	"memoria-backend/internal/auth"
	"memoria-backend/internal/billing"
	"memoria-backend/internal/capsules"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/exports"
	"memoria-backend/internal/friends"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/mailer"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/social"
	"memoria-backend/internal/stats"
	"memoria-backend/internal/users"
	"memoria-backend/internal/widget"
)

type Server struct {
	pool    *pgxpool.Pool
	cfg     *config.Config
	limiter *auth.Limiter
	auth    *auth.Handler
	users   *users.Handler
	media   *media.Handler
	friends *friends.Handler
	albums  *albums.Handler
	capsules *capsules.Handler
	social  *social.Handler
	notif   *notifications.Handler
	billing *billing.Handler
	stats   *stats.Handler
	exports *exports.Handler
	widget  *widget.Handler
}

// Deps holds optional feature wiring. Nil Notify falls back to LogSender.
type Deps struct {
	Notify       notifications.Sender
	Notif        *notifications.Handler
	ExportWorker *exports.Worker
}

func New(pool *pgxpool.Pool, cfg *config.Config, m mailer.Mailer, store *media.Store, deps *Deps) *Server {
	q := dbgen.New(pool)
	notify := notifications.Sender(notifications.LogSender{})
	var notif *notifications.Handler
	if deps != nil {
		if deps.Notify != nil {
			notify = deps.Notify
		}
		notif = deps.Notif
	}
	limiter := auth.NewLimiter()
	srv := &Server{
		pool:    pool,
		cfg:     cfg,
		limiter: limiter,
		auth: &auth.Handler{
			Q:       q,
			Mailer:  m,
			Secret:  []byte(cfg.JWTSecret),
			Limiter: limiter,
		},
		users: &users.Handler{Pool: pool, Q: q, Media: store},
		media: &media.Handler{Q: q, Store: store, Env: cfg.Env},
		friends: &friends.Handler{
			Pool: pool, Q: q, Media: store, Notify: notify,
		},
		albums: &albums.Handler{
			Pool: pool, Q: q, Media: store, Notify: notify,
		},
		capsules: &capsules.Handler{
			Pool: pool, Q: q, Media: store, Notify: notify,
		},
		billing: &billing.Handler{
			Pool: pool, Q: q, Cfg: cfg,
			OnPlanUpgrade: func(ctx context.Context, q *dbgen.Queries, userID uuid.UUID, plan billing.Plan) error {
				return capsules.RestampViewableForCreator(ctx, q, userID, plan)
			},
		},
		social: &social.Handler{Q: q, Notify: notify},
		notif:  notif,
		stats:  &stats.Handler{Q: q},
		widget: &widget.Handler{Q: q, Store: store},
	}
	if deps != nil && deps.ExportWorker != nil {
		ew := deps.ExportWorker
		srv.exports = &exports.Handler{
			Pool: pool, Q: q, Store: store, Notifier: ew.Notifier,
			Process: func(_ context.Context, jobID uuid.UUID) {
				ew.ProcessJobAsync(jobID)
			},
		}
	}
	return srv
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(slogLogger)
	r.Use(panicRecoverer)
	r.Use(RateLimitIP(s.limiter, s.cfg.Env))
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-Memoria-Media-Endpoint"},
		MaxAge:         300,
	}))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusMethodNotAllowed, httpx.CodeBadRequest, "method not allowed")
	})

	r.Get("/.well-known/apple-app-site-association", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		httpx.JSON(w, http.StatusOK, map[string]any{
			"applinks": map[string]any{
				"apps": []string{},
				"details": []map[string]any{
					{
						"appID": "WCY5X45K3C.com.pilar.memoria.app",
						"paths": []string{"*"},
					},
				},
			},
		})
	})

	r.Get("/.well-known/assetlinks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		httpx.JSON(w, http.StatusOK, []map[string]any{
			{
				"relation": []string{"delegate_permission/common.handle_all_urls"},
				"target": map[string]any{
					"namespace": "android_app",
					"package_name": "com.pilar.memoria.app",
					"sha256_cert_fingerprints": []string{
						"D8:C8:89:3A:0D:FF:48:5B:3C:84:CD:2B:CB:22:EE:50:1C:0A:DD:FE:A0:85:45:60:AB:6F:57:B5:32:04:35:46",
					},
				},
			},
		})
	})

	// Liveness: process is up.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Readiness: dependencies are reachable.
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, httpx.CodeInternal, "database unreachable")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	// Versioned API. Feature modules (auth, capsules, albums, ...) mount here.
	r.Route("/v1", func(v1 chi.Router) {
		s.auth.Mount(v1)
		s.billing.MountWebhook(v1)

		// Authenticated routes.
		v1.Group(func(p chi.Router) {
			p.Use(auth.RequireAuth([]byte(s.cfg.JWTSecret)))
			p.Use(RateLimitUser(s.limiter, s.cfg.Env))
			p.Use(media.PublicEndpointMiddleware(s.cfg.Env))
			s.users.Mount(p)
			s.billing.MountAuthed(p)
			s.media.Mount(p)
			s.friends.Mount(p)
			s.albums.Mount(p)
			s.capsules.Mount(p)
			s.social.Mount(p)
			if s.notif != nil {
				s.notif.Mount(p)
			}
			if s.stats != nil {
				s.stats.Mount(p)
			}
			if s.exports != nil {
				s.exports.Mount(p)
			}
			s.widget.Mount(p)
		})
	})

	return r
}
