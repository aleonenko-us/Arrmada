package webui

// placeholderHTML is shown when the backend runs without an embedded web build.
// It uses the Arrmada design tokens (warm terracotta) so a bare `go run` already
// looks like the product. The real UI (web/) replaces this once built.
const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Arrmada</title>
<style>
  :root { color-scheme: dark light; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center;
    background: #181310; color: #F0E7DC;
    font-family: -apple-system, "Segoe UI", Roboto, system-ui, sans-serif;
  }
  @media (prefers-color-scheme: light) { body { background: #F4EEE4; color: #2B2016; } }
  .card { text-align: center; padding: 40px; max-width: 460px; }
  .mark {
    width: 56px; height: 56px; margin: 0 auto 22px; border-radius: 15px;
    background: linear-gradient(150deg, #DB7A54, #C4633C);
    display: grid; place-items: center; box-shadow: 0 10px 30px -10px #DB7A54;
  }
  .mark svg { width: 32px; height: 32px; color: #241007; }
  h1 { font-size: 22px; font-weight: 800; letter-spacing: .14em; margin: 0 0 6px; }
  .tag { font-size: 13px; color: #BEAC98; margin: 0 0 24px; }
  @media (prefers-color-scheme: light) { .tag { color: #6C5D4E; } }
  .status {
    font-family: ui-monospace, "SF Mono", Consolas, monospace; font-size: 12px;
    display: inline-flex; align-items: center; gap: 9px;
    background: rgba(219,122,84,.14); color: #DB7A54;
    border: 1px solid rgba(219,122,84,.42); padding: 8px 14px; border-radius: 999px;
  }
  @media (prefers-color-scheme: light) { .status { color: #C15E39; background: rgba(193,94,57,.1); border-color: rgba(193,94,57,.35); } }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: currentColor; }
  .links { margin-top: 22px; font-size: 12.5px; }
  a { color: #DB7A54; text-decoration: none; }
  @media (prefers-color-scheme: light) { a { color: #C15E39; } }
  a:hover { text-decoration: underline; }
  .note { margin-top: 26px; font-size: 11.5px; color: #8C7A67; line-height: 1.5; }
</style>
</head>
<body>
  <div class="card">
    <div class="mark">
      <svg viewBox="72 72 368 368" fill="none">
        <path d="M244 92 L244 352 L128 352 Q176 212 244 92 Z" fill="currentColor" opacity="0.72"/>
        <path d="M268 92 L268 352 L384 352 Q336 212 268 92 Z" fill="currentColor"/>
        <path d="M128 376 L384 376 Q368 420 330 420 L182 420 Q144 420 128 376 Z" fill="currentColor"/>
      </svg>
    </div>
    <h1>ARRMADA</h1>
    <p class="tag">One fleet. Every media job.</p>
    <span class="status"><span class="dot"></span>Backend online — web UI not built yet</span>
    <div class="links"><a href="api/health">/api/health</a> &nbsp;·&nbsp; <a href="api/v1/status">/api/v1/status</a></div>
    <p class="note">This is the M0 foundation. Run the web build to replace this page.</p>
  </div>
</body>
</html>`
