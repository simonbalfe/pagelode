const tokenInput = document.querySelector("#token");
const health = document.querySelector("#health");
const sessionRows = document.querySelector("#sessions");
const sessionForm = document.querySelector("#session-form");
const sessionError = document.querySelector("#session-error");
const renderForm = document.querySelector("#render-form");
const renderError = document.querySelector("#render-error");
const renderResult = document.querySelector("#render-result");

tokenInput.value = localStorage.getItem("browser-service-token") ?? "";
tokenInput.addEventListener("change", () => {
  localStorage.setItem("browser-service-token", tokenInput.value);
  void refresh();
});

async function api(path, options = {}) {
  const headers = { "content-type": "application/json" };
  if (tokenInput.value !== "") headers.authorization = `Bearer ${tokenInput.value}`;
  const response = await fetch(path, { ...options, headers });
  if (response.status === 204) return undefined;
  const body = await response.json();
  if (!response.ok) throw new Error(body.error?.message ?? `request failed with ${response.status}`);
  return body;
}

function cell(content, className) {
  const td = document.createElement("td");
  if (className !== undefined) td.className = className;
  if (content instanceof Node) td.append(content);
  else td.textContent = content;
  return td;
}

function sessionRow(session) {
  const row = document.createElement("tr");
  const cdp = document.createElement("div");
  cdp.className = "cdp";
  const code = document.createElement("code");
  code.textContent = session.cdp_url;
  code.title = session.cdp_url;
  const copy = document.createElement("button");
  copy.type = "button";
  copy.className = "secondary";
  copy.textContent = "Copy";
  copy.addEventListener("click", async () => {
    await navigator.clipboard.writeText(tokenInput.value === "" ? session.cdp_url : `${session.cdp_url}?token=${encodeURIComponent(tokenInput.value)}`);
    copy.textContent = "Copied";
    setTimeout(() => {
      copy.textContent = "Copy";
    }, 1200);
  });
  cdp.append(code, copy);
  const close = document.createElement("button");
  close.type = "button";
  close.className = "danger";
  close.textContent = "Close";
  close.addEventListener("click", async () => {
    close.disabled = true;
    try {
      await api(`/v1/sessions/${session.id}`, { method: "DELETE" });
    } catch (error) {
      alert(error.message);
    }
    await refresh();
  });
  row.append(
    cell(session.id.slice(0, 12), "mono"),
    cell(session.profile ?? "none"),
    cell(session.headless ? "headless" : "headed"),
    cell(new Date(session.expires_at).toLocaleTimeString()),
    cell(cdp),
    cell(close),
  );
  return row;
}

async function refresh() {
  try {
    const status = await fetch("/healthz").then((response) => response.json());
    health.textContent = `${status.active} rendering, ${status.waiting} queued, ${status.sessions} sessions`;
  } catch {
    health.textContent = "unreachable";
  }
  try {
    const { sessions } = await api("/v1/sessions");
    sessionRows.replaceChildren(...(sessions.length === 0
      ? [Object.assign(document.createElement("tr"), { innerHTML: '<td colspan="6" class="empty">No open sessions</td>' })]
      : sessions.map(sessionRow)));
  } catch (error) {
    sessionRows.replaceChildren(Object.assign(document.createElement("tr"), { innerHTML: '<td colspan="6" class="empty"></td>' }));
    sessionRows.querySelector("td").textContent = error.message;
  }
}

function formBody(form) {
  const body = {};
  for (const [name, value] of new FormData(form)) {
    if (typeof value === "string" && value.trim() !== "") body[name] = value.trim();
  }
  for (const input of form.querySelectorAll("input[type=checkbox]")) body[input.name] = input.checked;
  return body;
}

sessionForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  sessionError.hidden = true;
  const button = sessionForm.querySelector("button");
  button.disabled = true;
  const body = formBody(sessionForm);
  if (body.ttl_seconds !== undefined) body.ttl_seconds = Number(body.ttl_seconds);
  try {
    await api("/v1/sessions", { method: "POST", body: JSON.stringify(body) });
    await refresh();
  } catch (error) {
    sessionError.textContent = error.message;
    sessionError.hidden = false;
  } finally {
    button.disabled = false;
  }
});

renderForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  renderError.hidden = true;
  const button = renderForm.querySelector("button");
  button.disabled = true;
  button.textContent = "Rendering";
  const body = formBody(renderForm);
  body.capture = body.capture ? { wait_ms: 500 } : undefined;
  const started = performance.now();
  try {
    const result = await api("/v1/render", { method: "POST", body: JSON.stringify(body) });
    document.querySelector("#result-status").textContent = String(result.status_code);
    document.querySelector("#result-url").textContent = result.final_url;
    document.querySelector("#result-title").textContent = result.title || "untitled";
    document.querySelector("#result-size").textContent = `${(result.html.length / 1024).toFixed(1)} KB`;
    document.querySelector("#result-cookies").textContent = String(result.cookies.length);
    document.querySelector("#result-traffic").textContent = result.traffic === undefined ? "not captured" : `${result.traffic.entries.length} captured, ${result.traffic.dropped} dropped`;
    document.querySelector("#result-time").textContent = `${((performance.now() - started) / 1000).toFixed(1)} s`;
    document.querySelector("#result-preview").srcdoc = result.html;
    document.querySelector("#result-html").textContent = result.html;
    renderResult.hidden = false;
  } catch (error) {
    renderError.textContent = error.message;
    renderError.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "Render";
  }
});

void refresh();
setInterval(() => void refresh(), 5000);
