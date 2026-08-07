const POLL_INTERVAL_MS = 10000;

const cardsEl = document.getElementById("cards");
const lastPollEl = document.getElementById("last-poll");
const toastEl = document.getElementById("toast");

function fmtTime(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleTimeString("ru-RU", { hour12: false });
}

function statusLabel(status) {
  if (status === "online") return "online";
  if (status === "degraded") return "degraded";
  if (status === "offline") return "offline";
  return "checking…";
}

function checkTypeLabel(checkType) {
  if (checkType === "http") return "HTTP /health";
  return "TCP";
}

function escapeHtml(str) {
  return String(str).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  }[c]));
}

function renderCards(services) {
  cardsEl.innerHTML = "";
  for (const svc of services) {
    const card = document.createElement("div");
    card.className = "card";
    card.dataset.status = svc.status;

    const actions = svc.actions || [];
    const buttonsHtml = actions
      .map(a => `<button class="trigger-btn" data-id="${svc.id}" data-action="${escapeHtml(a.id)}">${escapeHtml(a.label)}</button>`)
      .join("");

    card.innerHTML = `
      <div class="card-top">
        <div>
          <p class="card-name">${escapeHtml(svc.name)}</p>
          <p class="card-target">${escapeHtml(svc.host)}:${svc.port} <span class="check-type">${checkTypeLabel(svc.check_type)}</span></p>
        </div>
        <span class="status-pill"><span class="status-dot"></span>${statusLabel(svc.status)}</span>
      </div>
      ${svc.last_error ? `<div class="error-line">${escapeHtml(svc.last_error)}</div>` : ""}
      <div class="card-footer">
        <span class="last-checked">проверено: ${fmtTime(svc.last_checked)}${svc.response_time_ms != null ? ` · ${svc.response_time_ms}мс` : ""}</span>
        <div class="card-actions">${buttonsHtml}</div>
      </div>
    `;

    cardsEl.appendChild(card);
  }

  cardsEl.querySelectorAll(".trigger-btn").forEach((btn) => {
    btn.addEventListener("click", () => triggerWebhook(btn));
  });
}

async function pollStatus() {
  try {
    const res = await fetch("/api/status");
    if (!res.ok) throw new Error(`status ${res.status}`);
    const services = await res.json();
    renderCards(services);
    lastPollEl.textContent = "обновлено: " + new Date().toLocaleTimeString("ru-RU", { hour12: false });
  } catch (err) {
    lastPollEl.textContent = "ошибка опроса: " + err.message;
  }
}

async function triggerWebhook(btn) {
  const id = btn.dataset.id;
  const actionId = btn.dataset.action;
  btn.disabled = true;
  const originalText = btn.textContent;
  btn.textContent = "…";

  try {
    const res = await fetch(`/api/trigger/${encodeURIComponent(id)}/${encodeURIComponent(actionId)}`, { method: "POST" });
    const data = await res.json();
    showToast(data.success ? "success" : "error", data.message || (data.success ? "Запущено" : "Ошибка"));
  } catch (err) {
    showToast("error", "Не удалось выполнить запрос: " + err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = originalText;
  }
}

let toastTimer = null;
function showToast(type, message) {
  clearTimeout(toastTimer);
  toastEl.textContent = message;
  toastEl.className = `toast visible ${type}`;
  toastTimer = setTimeout(() => {
    toastEl.classList.remove("visible");
  }, 5000);
}

pollStatus();
setInterval(pollStatus, POLL_INTERVAL_MS);