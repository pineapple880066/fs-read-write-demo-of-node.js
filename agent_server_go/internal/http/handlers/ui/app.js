const STORAGE_THREADS = "local_agent_threads_v1";
const STORAGE_ACTIVE = "local_agent_active_thread_v1";

const state = {
  token: "",
  tenantId: "",
  userId: "",
  appName: "Local Codebase Agent",
  threads: [],
  activeThreadId: "",
  mode: "chat",
};

const els = {
  bootStatus: document.getElementById("boot-status"),
  newThreadBtn: document.getElementById("new-thread-btn"),
  focusInputBtn: document.getElementById("focus-input-btn"),
  threadList: document.getElementById("thread-list"),
  threadTitle: document.getElementById("thread-title"),
  threadSubtitle: document.getElementById("thread-subtitle"),
  globalRootInput: document.getElementById("global-root-input"),
  threadRootInput: document.getElementById("thread-root-input"),
  applyRootBtn: document.getElementById("apply-root-btn"),
  messages: document.getElementById("messages"),
  welcomeCard: document.getElementById("welcome-card"),
  composerInput: document.getElementById("composer-input"),
  sendBtn: document.getElementById("send-btn"),
  modelPill: document.getElementById("model-pill"),
  sessionPill: document.getElementById("session-pill"),
  composerRootHint: document.getElementById("composer-root-hint"),
  threadsRefreshBtn: document.getElementById("threads-refresh-btn"),
  chatScroll: document.getElementById("chat-scroll"),
  messageTemplate: document.getElementById("message-template"),
  modeButtons: Array.from(document.querySelectorAll(".mode-chip")),
};

function uid(prefix) {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

function nowLabel() {
  return new Date().toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

function loadThreads() {
  try {
    state.threads = JSON.parse(localStorage.getItem(STORAGE_THREADS) || "[]");
    state.activeThreadId = localStorage.getItem(STORAGE_ACTIVE) || "";
  } catch {
    state.threads = [];
    state.activeThreadId = "";
  }
}

function persistThreads() {
  localStorage.setItem(STORAGE_THREADS, JSON.stringify(state.threads));
  localStorage.setItem(STORAGE_ACTIVE, state.activeThreadId);
}

function activeThread() {
  return state.threads.find((t) => t.id === state.activeThreadId) || null;
}

function createThread(seedTitle = "New task") {
  const rootDir = (els.globalRootInput.value || "").trim();
  const thread = {
    id: uid("session"),
    title: seedTitle,
    subtitle: "等待输入问题",
    rootDir,
    mode: state.mode,
    createdAt: Date.now(),
    updatedAt: Date.now(),
    messages: [],
  };
  state.threads.unshift(thread);
  state.activeThreadId = thread.id;
  persistThreads();
  renderAll();
}

function renderThreadList() {
  els.threadList.innerHTML = "";
  state.threads.forEach((thread) => {
    const btn = document.createElement("button");
    btn.className = `thread-item${thread.id === state.activeThreadId ? " active" : ""}`;
    btn.innerHTML = `
      <h4>${escapeHTML(thread.title || "Untitled")}</h4>
      <p>${escapeHTML(thread.subtitle || "等待输入问题")}</p>
      <p>${escapeHTML(thread.rootDir || "No root directory")}</p>
    `;
    btn.addEventListener("click", () => {
      state.activeThreadId = thread.id;
      persistThreads();
      renderAll();
    });
    els.threadList.appendChild(btn);
  });
}

function renderHeader(thread) {
  els.threadTitle.textContent = thread ? thread.title : "New task";
  els.threadSubtitle.textContent = thread
    ? thread.subtitle || "本地代码问答，仅检索与回答，不修改文件"
    : "本地代码问答，仅检索与回答，不修改文件";
  els.threadRootInput.value = thread ? thread.rootDir || "" : "";
  els.sessionPill.textContent = thread ? thread.id : "no-session";
  els.composerRootHint.textContent = thread && thread.rootDir ? thread.rootDir : "root_dir required";
  state.mode = (thread && thread.mode) || "chat";
  els.modeButtons.forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.mode === state.mode);
  });
}

function renderMessages(thread) {
  els.messages.innerHTML = "";
  const list = thread ? thread.messages : [];
  els.welcomeCard.style.display = list.length ? "none" : "block";
  list.forEach((msg) => els.messages.appendChild(buildMessageNode(msg)));
  queueMicrotask(() => {
    els.chatScroll.scrollTop = els.chatScroll.scrollHeight;
  });
}

function buildMessageNode(msg) {
  const node = els.messageTemplate.content.firstElementChild.cloneNode(true);
  node.classList.add(msg.role);
  if (msg.busy) {
    node.classList.add("busy");
  }
  node.querySelector(".role").textContent = msg.role === "user" ? "user" : "assistant";
  node.querySelector(".time").textContent = msg.time || nowLabel();
  node.querySelector(".content").textContent = msg.content || "";

  const evidenceEl = node.querySelector(".evidence");
  (msg.evidenceFiles || []).forEach((file) => {
    const chip = document.createElement("span");
    chip.className = "evidence-chip";
    chip.textContent = file;
    evidenceEl.appendChild(chip);
  });

  const debugEl = node.querySelector(".debug");
  if (msg.debug) {
    debugEl.querySelector("pre").textContent = JSON.stringify(msg.debug, null, 2);
  } else {
    debugEl.remove();
  }
  return node;
}

function renderAll() {
  if (!state.threads.length) {
    createThread("Locate codebase");
    return;
  }
  if (!activeThread()) {
    state.activeThreadId = state.threads[0].id;
    persistThreads();
  }
  const thread = activeThread();
  renderThreadList();
  renderHeader(thread);
  renderMessages(thread);
}

async function bootstrap() {
  const res = await fetch("/ui/bootstrap");
  if (!res.ok) {
    throw new Error(`bootstrap failed: ${res.status}`);
  }
  const json = await res.json();
  state.token = json.data.token;
  state.tenantId = json.data.tenant_id;
  state.userId = json.data.user_id;
  state.appName = json.data.app_name || state.appName;
  els.bootStatus.textContent = "online";
  els.modelPill.textContent = state.appName;
}

function patchThread(threadId, updater) {
  const idx = state.threads.findIndex((t) => t.id === threadId);
  if (idx < 0) return null;
  const next = updater({ ...state.threads[idx] });
  state.threads[idx] = next;
  state.threads.sort((a, b) => b.updatedAt - a.updatedAt);
  persistThreads();
  renderAll();
  return next;
}

function setThreadRoot() {
  const thread = activeThread();
  if (!thread) return;
  patchThread(thread.id, (current) => ({
    ...current,
    rootDir: (els.threadRootInput.value || "").trim(),
    updatedAt: Date.now(),
  }));
}

function pushMessage(threadId, msg) {
  patchThread(threadId, (thread) => ({
    ...thread,
    subtitle: msg.role === "user" ? msg.content.slice(0, 42) : thread.subtitle,
    title: thread.messages.length === 0 && msg.role === "user" ? shrinkTitle(msg.content) : thread.title,
    updatedAt: Date.now(),
    messages: [...thread.messages, msg],
  }));
}

function replaceLastBusyMessage(threadId, msg) {
  patchThread(threadId, (thread) => {
    const messages = [...thread.messages];
    const idx = messages.findIndex((item) => item.busy);
    if (idx >= 0) {
      messages[idx] = msg;
    } else {
      messages.push(msg);
    }
    return {
      ...thread,
      updatedAt: Date.now(),
      messages,
      subtitle: msg.content.slice(0, 60),
    };
  });
}

function shrinkTitle(input) {
  const text = input.trim();
  if (!text) return "New task";
  return text.length > 32 ? `${text.slice(0, 32)}...` : text;
}

async function sendMessage(questionOverride) {
  const thread = activeThread();
  if (!thread) return;
  const question = (questionOverride || els.composerInput.value || "").trim();
  const rootDir = (thread.rootDir || "").trim();
  if (!question) return;
  if (!rootDir) {
    els.bootStatus.textContent = "root_dir missing";
    return;
  }

  els.composerInput.value = "";
  const userMsg = { id: uid("msg"), role: "user", content: question, time: nowLabel() };
  pushMessage(thread.id, userMsg);
  pushMessage(thread.id, { id: uid("msg"), role: "assistant", content: thread.mode === "edit" ? "正在检索代码并准备修改文件..." : "正在扫描目录并请求模型回答...", time: nowLabel(), busy: true });

  try {
    const res = await fetch("/v1/chat", {
      method: "POST",
      headers: {
        "Authorization": `Bearer ${state.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        tenant_id: state.tenantId,
        session_id: thread.id,
        user_id: state.userId,
        message: question,
        mode: thread.mode || "chat",
        root_dir: rootDir,
      }),
    });

    const payload = await res.json();
    if (!res.ok) {
      throw new Error(payload.message || payload.code || `HTTP ${res.status}`);
    }

    const data = payload.data;
    replaceLastBusyMessage(thread.id, {
      id: uid("msg"),
      role: "assistant",
      content: data.answer || "empty response",
      evidenceFiles: [
        ...(data.evidence_files || []),
        ...((data.changed_files || []).map((file) => `edited: ${file}`)),
      ],
      debug: data.retrieval_debug || null,
      time: nowLabel(),
    });
    els.bootStatus.textContent = "answered";
  } catch (err) {
    replaceLastBusyMessage(thread.id, {
      id: uid("msg"),
      role: "assistant",
      content: `请求失败: ${err.message}`,
      time: nowLabel(),
    });
    els.bootStatus.textContent = "request failed";
  }
}

function focusComposer() {
  els.composerInput.focus();
}

function autoGrow() {
  els.composerInput.style.height = "auto";
  els.composerInput.style.height = `${Math.min(220, els.composerInput.scrollHeight)}px`;
}

function escapeHTML(str) {
  return String(str)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function bindEvents() {
  els.newThreadBtn.addEventListener("click", () => createThread("New task"));
  els.focusInputBtn.addEventListener("click", focusComposer);
  els.applyRootBtn.addEventListener("click", setThreadRoot);
  els.threadsRefreshBtn.addEventListener("click", renderAll);
  els.composerInput.addEventListener("input", autoGrow);
  els.composerInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      sendMessage();
    }
  });
  els.sendBtn.addEventListener("click", () => sendMessage());
  els.globalRootInput.addEventListener("change", () => {
    const thread = activeThread();
    if (!thread || thread.rootDir) return;
    els.threadRootInput.value = els.globalRootInput.value;
    setThreadRoot();
  });
  document.querySelectorAll(".suggestion").forEach((btn) => {
    btn.addEventListener("click", () => sendMessage(btn.dataset.question || ""));
  });
  els.modeButtons.forEach((btn) => {
    btn.addEventListener("click", () => {
      const thread = activeThread();
      if (!thread) return;
      patchThread(thread.id, (current) => ({
        ...current,
        mode: btn.dataset.mode || "chat",
        updatedAt: Date.now(),
      }));
    });
  });
}

async function main() {
  loadThreads();
  bindEvents();
  renderAll();
  autoGrow();
  try {
    await bootstrap();
  } catch (err) {
    els.bootStatus.textContent = "bootstrap failed";
    console.error(err);
  }
  focusComposer();
}

main();
