const appName = process.env.APP_NAME || "upstream";
const port = Number(process.env.PORT || 3000);

function json(data, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      "content-type": "application/json",
    },
  });
}

Bun.serve({
  port,
  fetch(req) {
    const url = new URL(req.url);

    if (url.pathname === "/health") {
      return json({ status: "ok", app: appName });
    }

    const delayMs = Number(url.searchParams.get("delay_ms") || "0");
    const status = Number(url.searchParams.get("status") || "200");

    const respond = () =>
      json(
        {
          app: appName,
          method: req.method,
          path: url.pathname,
          query: Object.fromEntries(url.searchParams.entries()),
          now: new Date().toISOString(),
        },
        Number.isFinite(status) ? status : 200,
      );

    if (delayMs > 0 && Number.isFinite(delayMs)) {
      return new Promise((resolve) => {
        setTimeout(() => resolve(respond()), delayMs);
      });
    }

    return respond();
  },
});

console.log(`[${appName}] listening on :${port}`);
