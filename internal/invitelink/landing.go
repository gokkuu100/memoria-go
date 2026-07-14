package invitelink

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Mount registers the public invite landing page used by Universal / App Links.
func Mount(r chi.Router) {
	r.Get("/user/{username}", serveLanding)
}

func serveLanding(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(chi.URLParam(r, "username"))
	invite := strings.TrimSpace(r.URL.Query().Get("invite"))
	if username == "" {
		http.NotFound(w, r)
		return
	}

	deepLink := fmt.Sprintf("memoria://user/%s", username)
	if invite != "" {
		deepLink += "?invite=" + invite
	}

	escUser := html.EscapeString(username)
	escDeep := html.EscapeString(deepLink)
	title := fmt.Sprintf("Connect with @%s on Memoria", escUser)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>%s</title>
  <meta property="og:title" content="%s"/>
  <meta property="og:description" content="Add @%s as a friend on Memoria — private capsules and albums."/>
  <style>
    * { box-sizing: border-box; }
    body {
      margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: radial-gradient(ellipse at 50%% 0%%, #2a1f18 0%%, #1c1410 55%%, #120d0a 100%%);
      color: #f5efe6; padding: 24px;
    }
    .card {
      max-width: 400px; width: 100%%; text-align: center;
      background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.1);
      border-radius: 20px; padding: 32px 24px;
    }
    h1 { font-size: 1.35rem; margin: 0 0 8px; font-weight: 600; }
    p { color: rgba(245,239,230,0.72); font-size: 0.95rem; line-height: 1.5; margin: 0 0 24px; }
    a.btn {
      display: inline-block; padding: 14px 28px; border-radius: 999px;
      background: #f5a623; color: #1c1410; font-weight: 600; text-decoration: none;
      font-size: 1rem;
    }
    .hint { margin-top: 20px; font-size: 0.8rem; color: rgba(245,239,230,0.45); }
  </style>
</head>
<body>
  <div class="card">
    <h1>@%s invited you</h1>
    <p>Open Memoria to view their profile and send a friend request.</p>
    <a class="btn" id="open" href="%s">Open in Memoria</a>
    <p class="hint">Don't have the app yet? Install Memoria from the App Store or Google Play, then tap this link again.</p>
  </div>
  <script>
    (function () {
      var deep = %q;
      var isMobile = /iPhone|iPad|iPod|Android/i.test(navigator.userAgent);
      if (!isMobile) return;
      setTimeout(function () {
        window.location.href = deep;
      }, 120);
    })();
  </script>
</body>
</html>`, title, title, escUser, escUser, escDeep, deepLink)
}
