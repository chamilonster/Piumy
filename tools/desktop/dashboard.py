# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Camilo Brossard
"""Piumy Desktop -- M2: pywebview launcher with dashboard auto-login.

The dashboard has no no-auth mode (core/internal/dashboard/dashboard.go) --
every path but /login redirects there without a valid session cookie. Since
LocalSource generated the sandbox's own username/password itself, there is
nothing secret to hide behind a manual login screen here: once the webview
lands on /login, this fills the form via JS and submits it. A webview loads
the dashboard as a top-level document (not an iframe), so the dashboard's
`X-Frame-Options: DENY` (which only blocks iframes) never comes into play.

Runs as its OWN process (see desktop.py's `_open_dashboard`), not a thread of
the main Tkinter app: pywebview's Windows backend raises
`WebViewException('pywebview must be run on a main thread')` if `start()`
isn't called from the process's main thread, and Tkinter's mainloop already
owns that thread in the main app.
"""
import webview

# document.getElementById + string literals (not an f-string on raw HTML) --
# username/password never contain a `'` (LocalSource generates the password,
# PIMYWA_DASH_USER is hardcoded "admin"), but escaping defensively costs
# nothing and avoids a broken script if that ever changes.
_LOGIN_JS = """
(function() {{
  var u = document.getElementById('u');
  var p = document.getElementById('p');
  if (u && p) {{
    u.value = '{user}';
    p.value = '{password}';
    u.form.submit();
  }}
}})();
"""


def _esc(s: str) -> str:
    return s.replace("\\", "\\\\").replace("'", "\\'")


def open_dashboard(url: str, username: str, password: str) -> None:
    """Open the dashboard in a pywebview window, auto-submitting the login
    form the first time it lands on /login. Blocks until the window closes
    -- call this as the entire body of a dedicated process (see desktop.py's
    `_open_dashboard`), never from a thread inside the main Tkinter app."""
    window = webview.create_window("Piumy Dashboard", url, width=1000, height=750)

    def _on_loaded():
        try:
            if window.get_current_url().split("?")[0].rstrip("/").endswith("/login"):
                window.evaluate_js(_LOGIN_JS.format(user=_esc(username), password=_esc(password)))
        except Exception:
            pass  # best-effort -- a failed auto-fill just leaves the login form visible

    window.events.loaded += _on_loaded
    webview.start()
