(() => {
  "use strict";

  // ---------- Token bootstrap ----------

  const hashToken = window.location.hash.startsWith("#") ? window.location.hash.slice(1) : "";
  if (hashToken) {
    window.history.replaceState(null, "", window.location.pathname);
  }
  const token = hashToken;

  const locked = document.querySelector("#locked");
  const app = document.querySelector("#app");
  if (!token) {
    locked.hidden = false;
    return;
  }
  app.hidden = false;

  const elements = {
    app,
    backButton: document.querySelector("#back-button"),
    chatAvatar: document.querySelector("#chat-avatar"),
    chatFlag: document.querySelector("#chat-flag"),
    chatIntro: document.querySelector("#chat-intro"),
    chatList: document.querySelector("#chat-list"),
    chatSubtitle: document.querySelector("#chat-subtitle"),
    chatTitle: document.querySelector("#chat-title"),
    chatView: document.querySelector("#chat-view"),
    filterChips: document.querySelector("#filter-chips"),
    introStats: document.querySelector("#intro-stats"),
    messageList: document.querySelector("#message-list"),
    messageScroll: document.querySelector("#message-scroll"),
    refresh: document.querySelector("#refresh-button"),
    searchClear: document.querySelector("#search-clear"),
    searchForm: document.querySelector("#search-form"),
    searchInput: document.querySelector("#search-input"),
    statsGrid: document.querySelector("#stats-grid"),
    statsPanel: document.querySelector("#stats-panel"),
    statsRange: document.querySelector("#stats-range"),
    statsToggle: document.querySelector("#stats-toggle"),
    syncNote: document.querySelector("#sync-note"),
    themeToggle: document.querySelector("#theme-toggle"),
    toast: document.querySelector("#toast"),
  };

  const FETCH_LIMIT = 300;
  const RUN_GAP_MS = 10 * 60 * 1000;
  const SNIPPET_START = "\ue000";
  const SNIPPET_END = "\ue001";

  const state = {
    chats: [],
    filter: "all",
    messages: [],
    seenMessageIds: new Set(),
    selectedChat: null,
    searching: false,
    status: null,
    theme: "auto",
    viewRequest: 0,
  };

  // ---------- Icons (inline SVG, no external assets) ----------

  const ICONS = {
    audio: "M12 14a3 3 0 0 0 3-3V6a3 3 0 1 0-6 0v5a3 3 0 0 0 3 3Zm5.6-3a.9.9 0 0 0-1.8.2 3.8 3.8 0 0 1-7.6 0A.9.9 0 0 0 6.4 11a5.6 5.6 0 0 0 4.7 5.3V19H9a.9.9 0 1 0 0 1.8h6a.9.9 0 1 0 0-1.8h-2.1v-2.7A5.6 5.6 0 0 0 17.6 11Z",
    back: "M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20v-2Z",
    chart: "M5 9.2h3V19H5V9.2ZM10.6 5h2.8v14h-2.8V5Zm5.6 8H19v6h-2.8v-6Z",
    contact: "M12 12a4 4 0 1 0-4-4 4 4 0 0 0 4 4Zm0 2c-3.3 0-8 1.7-8 5v2h16v-2c0-3.3-4.7-5-8-5Z",
    document: "M14 2H7a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7l-5-5Zm-1 6V3.8L17.2 8H13Zm-4 4h6v1.6H9V12Zm0 3.4h6V17H9v-1.6Z",
    empty: "M12 3C6.9 3 3 6.6 3 11c0 1.9.7 3.7 1.9 5.1L4 20.5l4.7-1.6c1 .3 2.1.5 3.3.5 5.1 0 9-3.6 9-8.4S17.1 3 12 3Z",
    gif: "M4 5h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1Zm4.2 5.4H6.4v3.2h.9v-1h.9v2H6a1 1 0 0 1-1-1v-3.2a1 1 0 0 1 1-1h2.2v1Zm2.7-1v5.2h-1.5V9.4h1.5Zm1.4 0H16v1h-2.2v1.1H16v1h-2.2v2.1h-1.5V9.4Z",
    image: "M19 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2Zm-9.5 5A1.5 1.5 0 1 1 8 9.5 1.5 1.5 0 0 1 9.5 8Zm9.5 11H5l4.5-6 2.8 3.4L15.5 12 19 19Z",
    link: "M10.6 13.4a1 1 0 0 0 1.4 1.4l4.5-4.5a3 3 0 0 0-4.3-4.3L9.6 8.6A1 1 0 1 0 11 10l2.6-2.6a1 1 0 0 1 1.4 1.4l-4.4 4.6ZM13.4 10.6a1 1 0 0 0-1.4-1.4l-4.5 4.5a3 3 0 0 0 4.3 4.3l2.6-2.6a1 1 0 1 0-1.4-1.4L10.4 16.6a1 1 0 0 1-1.4-1.4l4.4-4.6Z",
    location: "M12 2a7 7 0 0 0-7 7c0 5.2 7 13 7 13s7-7.8 7-13a7 7 0 0 0-7-7Zm0 9.5A2.5 2.5 0 1 1 14.5 9 2.5 2.5 0 0 1 12 11.5Z",
    lock: "M12 2a5 5 0 0 0-5 5v3H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8a2 2 0 0 0-2-2h-1V7a5 5 0 0 0-5-5Zm-3 8V7a3 3 0 1 1 6 0v3H9Zm3 4a1.5 1.5 0 0 1 .75 2.8V19a.75.75 0 0 1-1.5 0v-2.2A1.5 1.5 0 0 1 12 14Z",
    megaphone: "M18 8a3 3 0 0 1 0 6v3.5a1 1 0 0 1-1.6.8L12 15H6a2 2 0 0 1-2-2v-2a2 2 0 0 1 2-2h6l4.4-3.3A1 1 0 0 1 18 6.5V8Zm-11 8h3l.8 3.2a1 1 0 0 1-1 1.2H8.6a1 1 0 0 1-1-.8L7 16Z",
    moon: "M12.3 3a9 9 0 1 0 8.7 11.3A7.2 7.2 0 0 1 12.3 3Z",
    people: "M9 11a3.5 3.5 0 1 0-3.5-3.5A3.5 3.5 0 0 0 9 11Zm7 0a3 3 0 1 0-3-3 3 3 0 0 0 3 3Zm-7 2c-2.7 0-7 1.3-7 4v2h11v-2c0-2.7-1.3-4-4-4Zm7 .4a6.6 6.6 0 0 1 3 5.6v1h3v-2c0-2.3-3.4-4.2-6-4.6Z",
    person: "M12 12a4.5 4.5 0 1 0-4.5-4.5A4.5 4.5 0 0 0 12 12Zm0 2.2c-3.6 0-9 1.8-9 5.3V21h18v-1.5c0-3.5-5.4-5.3-9-5.3Z",
    refresh: "M17.65 6.35A8 8 0 1 0 19.73 14h-2.08a6 6 0 1 1-1.42-6.23L13 11h7V4l-2.35 2.35Z",
    search: "M15.5 14h-.79l-.28-.27a6.5 6.5 0 1 0-.7.7l.27.28v.79l5 4.99L20.49 19l-4.99-5Zm-6 0A4.5 4.5 0 1 1 14 9.5 4.5 4.5 0 0 1 9.5 14Z",
    spinner: "M12 4a8 8 0 1 0 8 8h-2.2A5.8 5.8 0 1 1 12 6.2V4Z",
    sticker: "M5 3h14a2 2 0 0 1 2 2v9l-7 7H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Zm9 16 5-5h-4a1 1 0 0 0-1 1v4ZM8.2 9.4a1.2 1.2 0 1 0-1.2-1.2 1.2 1.2 0 0 0 1.2 1.2Zm7.6 0a1.2 1.2 0 1 0-1.2-1.2 1.2 1.2 0 0 0 1.2 1.2Zm-7.5 3a4.3 4.3 0 0 0 7.4 0l-1.3-.8a2.8 2.8 0 0 1-4.8 0Z",
    sun: "M12 7a5 5 0 1 0 5 5 5 5 0 0 0-5-5Zm0-5.5 1.7 3.2H10.3L12 1.5Zm0 21-1.7-3.2h3.4L12 22.5ZM1.5 12l3.2-1.7v3.4L1.5 12Zm21 0-3.2 1.7v-3.4l3.2 1.7ZM4 5.2l3.4 1L5.2 8.5 4 5.2Zm16 13.6-3.4-1 2.2-2.3 1.2 3.3ZM4 18.8l1.2-3.3 2.2 2.3-3.4 1ZM20 5.2l-1.2 3.3-2.2-2.3 3.4-1Z",
    themeAuto: "M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2Zm0 18V4a8 8 0 0 1 0 16Z",
    video: "M17 10.5V6a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-4.5l4 4v-11l-4 4Z",
  };

  function icon(name, className) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    if (className) svg.setAttribute("class", className);
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", ICONS[name] || ICONS.empty);
    svg.append(path);
    return svg;
  }

  function tickIcon() {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 18 12");
    svg.setAttribute("aria-label", "sent");
    for (const d of ["M1.3 6.6 4.6 9.9 10.6 2.6", "M7.3 6.6 10.6 9.9 16.6 2.6"]) {
      const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      path.setAttribute("d", d);
      path.setAttribute("fill", "none");
      path.setAttribute("stroke", "currentColor");
      path.setAttribute("stroke-width", "1.7");
      path.setAttribute("stroke-linecap", "round");
      path.setAttribute("stroke-linejoin", "round");
      svg.append(path);
    }
    return svg;
  }

  function starIcon() {
    const span = document.createElement("span");
    span.className = "star";
    span.title = "Starred";
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M12 2.7l2.9 5.9 6.5.9-4.7 4.6 1.1 6.4-5.8-3-5.8 3 1.1-6.4L2.6 9.5l6.5-.9L12 2.7Z");
    svg.append(path);
    span.append(svg);
    return span;
  }

  function tailSvg() {
    const span = document.createElement("span");
    span.className = "bubble-tail";
    span.setAttribute("aria-hidden", "true");
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 8 13");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M8 0H2.6C0.9 0 0.4 1.2 1.5 2.6L8 11V0Z");
    svg.append(path);
    span.append(svg);
    return span;
  }

  // The base path hugs the right edge of its box with the point at the top
  // left, which is the incoming orientation; mirror it for outgoing bubbles.
  function tailFor(mine) {
    const tail = tailSvg();
    if (mine) tail.querySelector("path").setAttribute("transform", "scale(-1 1) translate(-8 0)");
    return tail;
  }

  // ---------- Theme ----------

  const root = document.documentElement;
  const darkQuery = window.matchMedia("(prefers-color-scheme: dark)");
  const THEMES = ["auto", "dark", "light"];

  function applyTheme() {
    root.dataset.theme = state.theme;
    root.classList.toggle("prefers-dark", darkQuery.matches);
    elements.themeToggle.replaceChildren(icon(state.theme === "dark" ? "moon" : state.theme === "light" ? "sun" : "themeAuto"));
    elements.themeToggle.title = `Theme: ${state.theme} — click to switch`;
  }

  try {
    const saved = window.localStorage.getItem("wacrawl-theme");
    if (THEMES.includes(saved)) state.theme = saved;
  } catch { /* storage unavailable — stay on auto */ }
  darkQuery.addEventListener("change", applyTheme);
  elements.themeToggle.addEventListener("click", () => {
    state.theme = THEMES[(THEMES.indexOf(state.theme) + 1) % THEMES.length];
    try { window.localStorage.setItem("wacrawl-theme", state.theme); } catch { /* best effort */ }
    applyTheme();
  });
  applyTheme();

  // ---------- Static chrome icons ----------

  elements.statsToggle.append(icon("chart"));
  elements.refresh.append(icon("refresh"));
  elements.backButton.append(icon("back"));
  document.querySelector(".search-icon").append(icon("search"));
  document.querySelectorAll(".foot-icon").forEach((node) => node.append(icon("lock")));

  // ---------- API ----------

  async function api(path) {
    const response = await window.fetch(path, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (response.status === 401) {
      throw new Error("The private viewer key expired. Restart wacrawl web.");
    }
    if (!response.ok) {
      const detail = (await response.text()).trim();
      throw new Error(detail || `Request failed (${response.status})`);
    }
    return response.json();
  }

  // ---------- Formatting helpers ----------

  const numberFormat = new Intl.NumberFormat();

  function formatCount(value) {
    return numberFormat.format(Number(value || 0));
  }

  function parseDate(value) {
    if (!value || value.startsWith("0001-")) return null;
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date;
  }

  function sameDay(a, b) {
    return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  }

  function daysAgo(date) {
    const today = new Date();
    const startOfToday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
    const startOfDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
    return Math.round((startOfToday - startOfDate) / 86400000);
  }

  function formatClock(date) {
    return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  }

  function formatListTime(value) {
    const date = parseDate(value);
    if (!date) return "";
    const age = daysAgo(date);
    if (age <= 0) return formatClock(date);
    if (age === 1) return "Yesterday";
    if (age < 7) return new Intl.DateTimeFormat(undefined, { weekday: "long" }).format(date);
    return new Intl.DateTimeFormat(undefined, { dateStyle: "short" }).format(date);
  }

  function formatDayLabel(date) {
    if (!date) return "Undated";
    const age = daysAgo(date);
    if (age <= 0) return "Today";
    if (age === 1) return "Yesterday";
    if (age < 7) return new Intl.DateTimeFormat(undefined, { weekday: "long" }).format(date);
    return new Intl.DateTimeFormat(undefined, { dateStyle: "long" }).format(date);
  }

  function formatDay(value) {
    const date = parseDate(value);
    if (!date) return "unknown";
    return new Intl.DateTimeFormat(undefined, { month: "short", year: "numeric" }).format(date);
  }

  function formatStamp(value) {
    const date = parseDate(value);
    if (!date) return "";
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
  }

  function formatSize(bytes) {
    const value = Number(bytes || 0);
    if (value <= 0) return "";
    const units = ["B", "KB", "MB", "GB"];
    let unit = 0;
    let out = value;
    while (out >= 1024 && unit < units.length - 1) {
      out /= 1024;
      unit += 1;
    }
    return `${out >= 10 || unit === 0 ? Math.round(out) : out.toFixed(1)} ${units[unit]}`;
  }

  function hashCode(value) {
    let hash = 2166136261;
    for (let i = 0; i < value.length; i += 1) {
      hash ^= value.charCodeAt(i);
      hash = Math.imul(hash, 16777619);
    }
    return hash >>> 0;
  }

  function chatName(chat) {
    return chat.name || prettyJid(chat.jid) || "Untitled chat";
  }

  function prettyJid(jid) {
    if (!jid) return "";
    const bare = jid.split("@")[0].split(":")[0];
    if (/^\d{6,}$/.test(bare)) return `+${bare}`;
    return bare;
  }

  function senderLabel(message) {
    if (message.from_me) return "You";
    return message.sender_name || prettyJid(message.sender_jid) || "Unknown";
  }

  // ---------- Avatars ----------

  function initialsOf(name) {
    const words = (name || "").trim().split(/\s+/).filter(Boolean);
    let out = "";
    for (const word of words) {
      const letter = Array.from(word).find((ch) => /\p{L}|\p{N}/u.test(ch));
      if (letter) out += letter.toUpperCase();
      if (out.length === 2) break;
    }
    return out;
  }

  function avatarNode(seed, name, kind, size) {
    const span = document.createElement("span");
    span.className = `avatar av-${hashCode(seed || name || "?") % 8}`;
    if (size) span.classList.add(`size-${size}`);
    span.setAttribute("aria-hidden", "true");
    const letters = initialsOf(name);
    if (kind === "group") {
      span.append(icon("people"));
    } else if (kind === "newsletter") {
      span.append(icon("megaphone"));
    } else if (letters) {
      span.textContent = letters;
    } else {
      span.append(icon("person"));
    }
    return span;
  }

  // ---------- Rich text (WhatsApp formatting + light markdown) ----------
  // Everything is built with createElement/textContent — message content is
  // never parsed as HTML.

  const BOUNDARY_OPEN = "(^|[\\s({\\[\"'>—–-])";

  function inlinePattern(marker, escaped) {
    return new RegExp(
      `${BOUNDARY_OPEN}${escaped}([^\\s${marker}](?:[^${marker}\\n]*[^\\s${marker}])?)${escaped}(?=[\\s)}\\].,!?;:'"<»…]|$)`
    );
  }

  const INLINE_RULES = [
    { name: "code", re: /(^|[\s\S]?)```([^`\n]+)```/, group: 2, tag: "code", literal: true },
    { name: "code", re: /(^|[^`])`([^`\n]+)`(?!`)/, group: 2, tag: "code", literal: true },
    { name: "bold", re: /(^|[\s\S]?)\*\*([^\s*](?:[^*\n]*[^\s*])?)\*\*/, group: 2, tag: "strong" },
    { name: "bold", re: inlinePattern("*", "\\*"), group: 2, tag: "strong" },
    { name: "italic", re: inlinePattern("_", "_"), group: 2, tag: "em" },
    { name: "strike", re: /(^|[\s\S]?)~~([^\s~](?:[^~\n]*[^\s~])?)~~/, group: 2, tag: "s" },
    { name: "strike", re: inlinePattern("~", "~"), group: 2, tag: "s" },
  ];

  const URL_RE = /https?:\/\/[^\s<>"'`]+/;

  function appendInline(target, text) {
    let rest = text;
    while (rest.length) {
      let best = null;
      for (const rule of INLINE_RULES) {
        const match = rule.re.exec(rest);
        if (!match) continue;
        const prefix = match[1] || "";
        const index = match.index + prefix.length;
        if (!best || index < best.index) {
          best = { rule, match, index, prefix };
        }
      }
      const urlMatch = URL_RE.exec(rest);
      if (urlMatch) {
        let url = urlMatch[0];
        const trimmed = url.replace(/[.,;:!?)\]}'"…]+$/, "");
        if (!best || urlMatch.index < best.index) {
          if (urlMatch.index > 0) target.append(document.createTextNode(rest.slice(0, urlMatch.index)));
          const anchor = document.createElement("a");
          anchor.href = trimmed;
          anchor.target = "_blank";
          anchor.rel = "noopener noreferrer nofollow";
          anchor.textContent = trimmed;
          target.append(anchor);
          rest = rest.slice(urlMatch.index + trimmed.length);
          continue;
        }
      }
      if (!best) {
        target.append(document.createTextNode(rest));
        return;
      }
      const { rule, match, prefix } = best;
      const before = rest.slice(0, match.index) + prefix;
      if (before) target.append(document.createTextNode(before));
      const node = document.createElement(rule.tag);
      if (rule.literal) {
        node.textContent = match[rule.group];
      } else {
        appendInline(node, match[rule.group]);
      }
      target.append(node);
      rest = rest.slice(match.index + match[0].length);
    }
  }

  function renderRichText(raw) {
    const fragment = document.createDocumentFragment();
    const lines = String(raw).split("\n");
    let index = 0;
    let paragraph = [];

    function flushParagraph() {
      if (!paragraph.length) return;
      const p = document.createElement("p");
      paragraph.forEach((line, i) => {
        if (i > 0) p.append(document.createTextNode("\n"));
        appendInline(p, line);
      });
      fragment.append(p);
      paragraph = [];
    }

    while (index < lines.length) {
      const line = lines[index];
      const fence = /^```(\w*)\s*$/.exec(line);
      if (fence) {
        let end = index + 1;
        while (end < lines.length && !/^```\s*$/.test(lines[end])) end += 1;
        if (end < lines.length) {
          flushParagraph();
          const pre = document.createElement("pre");
          const code = document.createElement("code");
          code.textContent = lines.slice(index + 1, end).join("\n");
          pre.append(code);
          fragment.append(pre);
          index = end + 1;
          continue;
        }
      }
      if (/^>\s?/.test(line)) {
        flushParagraph();
        const quote = document.createElement("blockquote");
        let first = true;
        while (index < lines.length && /^>\s?/.test(lines[index])) {
          if (!first) quote.append(document.createTextNode("\n"));
          appendInline(quote, lines[index].replace(/^>\s?/, ""));
          first = false;
          index += 1;
        }
        fragment.append(quote);
        continue;
      }
      const bulletRe = /^\s{0,3}[-*•]\s+(.*)$/;
      if (bulletRe.test(line)) {
        flushParagraph();
        const list = document.createElement("ul");
        while (index < lines.length && bulletRe.test(lines[index])) {
          const item = document.createElement("li");
          appendInline(item, bulletRe.exec(lines[index])[1]);
          list.append(item);
          index += 1;
        }
        fragment.append(list);
        continue;
      }
      const orderedRe = /^\s{0,3}(\d{1,4})[.)]\s+(.*)$/;
      if (orderedRe.test(line)) {
        flushParagraph();
        const list = document.createElement("ol");
        list.start = Number(orderedRe.exec(line)[1]);
        while (index < lines.length && orderedRe.test(lines[index])) {
          const item = document.createElement("li");
          appendInline(item, orderedRe.exec(lines[index])[2]);
          list.append(item);
          index += 1;
        }
        fragment.append(list);
        continue;
      }
      if (line.trim() === "") {
        flushParagraph();
        index += 1;
        continue;
      }
      paragraph.push(line);
      index += 1;
    }
    flushParagraph();
    return fragment;
  }

  function renderSnippet(target, snippet) {
    let rest = snippet;
    while (rest.length) {
      const start = rest.indexOf(SNIPPET_START);
      if (start === -1) {
        appendInline(target, rest);
        return;
      }
      if (start > 0) appendInline(target, rest.slice(0, start));
      const end = rest.indexOf(SNIPPET_END, start + 1);
      if (end === -1) {
        target.append(document.createTextNode(rest.slice(start + 1)));
        return;
      }
      const mark = document.createElement("mark");
      mark.textContent = rest.slice(start + 1, end);
      target.append(mark);
      rest = rest.slice(end + 1);
    }
  }

  const EMOJI_ONLY_RE = /^(?:\p{Extended_Pictographic}|\p{Emoji_Modifier}|\p{Regional_Indicator}|[‍️\s])+$/u;

  function isJumboEmoji(text) {
    if (!text || !EMOJI_ONLY_RE.test(text)) return false;
    const glyphs = Array.from(text.trim()).filter((ch) => /\p{Extended_Pictographic}|\p{Regional_Indicator}/u.test(ch));
    return glyphs.length > 0 && glyphs.length <= 4;
  }

  // ---------- Toast ----------

  function showToast(message) {
    elements.toast.textContent = message;
    elements.toast.hidden = false;
    window.clearTimeout(showToast.timer);
    showToast.timer = window.setTimeout(() => {
      elements.toast.hidden = true;
    }, 4800);
  }

  // ---------- Status ----------

  function renderStatus(status) {
    state.status = status;
    const values = [status.messages, status.chats, status.contacts, status.groups, status.media_messages, status.unread_messages];
    elements.statsGrid.querySelectorAll("dd").forEach((node, i) => {
      node.textContent = formatCount(values[i]);
    });
    elements.statsRange.textContent = status.messages
      ? `Archive spans ${formatDay(status.oldest_message)} — ${formatDay(status.newest_message)}`
      : "Archive is empty — run wacrawl import.";
    const imported = formatStamp(status.last_import_at);
    elements.syncNote.textContent = imported ? `Snapshot ${imported} · loopback only` : "Not imported yet · loopback only";
    elements.introStats.textContent = status.messages
      ? `${formatCount(status.messages)} messages · ${formatCount(status.chats)} chats · ${formatDay(status.oldest_message)} — ${formatDay(status.newest_message)}`
      : "";
  }

  // ---------- Chat list ----------

  function visibleChats() {
    // While a message search is showing, the text belongs to that search, so
    // it should not also narrow the chat list.
    const query = state.searching ? "" : elements.searchInput.value.trim().toLowerCase();
    return state.chats.filter((chat) => {
      if (state.filter === "unread" && !(chat.unread_count > 0)) return false;
      if (state.filter === "groups" && chat.kind !== "group") return false;
      if (query && !chatName(chat).toLowerCase().includes(query) && !chat.jid.toLowerCase().includes(query)) return false;
      return true;
    });
  }

  function renderChats() {
    const chats = visibleChats();
    elements.chatList.replaceChildren();
    if (!chats.length) {
      const empty = document.createElement("div");
      empty.className = "empty-state";
      empty.append(icon("empty"));
      const text = document.createElement("p");
      text.textContent = state.chats.length ? "No chats match." : "No chats in this archive yet.";
      empty.append(text);
      elements.chatList.append(empty);
      return;
    }
    chats.forEach((chat, i) => {
      const row = document.createElement("button");
      row.type = "button";
      row.className = "chat-row";
      row.setAttribute("role", "option");
      row.setAttribute("aria-selected", String(!state.searching && state.selectedChat?.jid === chat.jid));
      row.title = chatName(chat);
      row.style.setProperty("--i", Math.min(i, 14));
      if (!state.searching && state.selectedChat?.jid === chat.jid) row.classList.add("active");

      row.append(avatarNode(chat.jid, chat.name, chat.kind, ""));

      const copy = document.createElement("span");
      copy.className = "chat-copy";

      const line = document.createElement("span");
      line.className = "chat-line";
      const name = document.createElement("span");
      name.className = "chat-name";
      name.textContent = chatName(chat);
      const time = document.createElement("span");
      time.className = "chat-time";
      if (chat.unread_count > 0) time.classList.add("unread-time");
      time.textContent = formatListTime(chat.last_message_at);
      line.append(name, time);

      const meta = document.createElement("span");
      meta.className = "chat-meta";
      const metaText = document.createElement("span");
      metaText.className = "chat-meta-text";
      metaText.textContent = `${formatCount(chat.message_count)} message${chat.message_count === 1 ? "" : "s"}`;
      meta.append(metaText);
      if (chat.kind && chat.kind !== "dm") {
        const tag = document.createElement("span");
        tag.className = "kind-tag";
        tag.textContent = chat.kind;
        meta.append(tag);
      }
      if (chat.archived) {
        const tag = document.createElement("span");
        tag.className = "kind-tag";
        tag.textContent = "archived";
        meta.append(tag);
      }
      if (chat.unread_count > 0) {
        const pill = document.createElement("span");
        pill.className = "unread-pill";
        pill.textContent = chat.unread_count > 99 ? "99+" : formatCount(chat.unread_count);
        pill.setAttribute("aria-label", `${chat.unread_count} unread`);
        meta.append(pill);
      }

      copy.append(line, meta);
      row.append(copy);
      row.addEventListener("click", () => selectChat(chat));
      elements.chatList.append(row);
    });
  }

  // ---------- Message rendering ----------

  function mediaKind(message) {
    const hint = `${message.media_type || ""} ${message.message_type || ""}`.toLowerCase();
    for (const kind of ["image", "video", "gif", "audio", "sticker", "document", "location", "contact", "link"]) {
      if (hint.includes(kind)) return kind;
    }
    if (hint.includes("voice") || hint.includes("ptt")) return "audio";
    if (hint.includes("photo")) return "image";
    return "document";
  }

  const MEDIA_PLACEHOLDER = {
    audio: "Voice message",
    contact: "Contact card",
    document: "Document",
    gif: "GIF",
    image: "Photo",
    link: "Link preview",
    location: "Location",
    sticker: "Sticker",
    video: "Video",
  };

  function mediaCard(message) {
    const kind = mediaKind(message);
    const card = document.createElement("span");
    card.className = "media-card";
    const badge = document.createElement("span");
    badge.className = "media-icon";
    badge.append(icon(kind));
    const copy = document.createElement("span");
    copy.className = "media-copy";
    const title = document.createElement("span");
    title.className = "media-title";
    title.textContent = message.media_title || MEDIA_PLACEHOLDER[kind] || "Attachment";
    copy.append(title);
    const noteText = [MEDIA_PLACEHOLDER[kind] || kind, formatSize(message.media_size)].filter(Boolean).join(" · ");
    if (noteText && noteText !== title.textContent) {
      const note = document.createElement("span");
      note.className = "media-note";
      note.textContent = noteText;
      copy.append(note);
    }
    card.append(badge, copy);
    return card;
  }

  function bubbleMeta(message) {
    const meta = document.createElement("span");
    meta.className = "bubble-meta";
    if (message.starred) meta.append(starIcon());
    const date = parseDate(message.timestamp);
    const time = document.createElement("time");
    if (date) {
      time.dateTime = message.timestamp;
      time.textContent = formatClock(date);
      time.title = formatStamp(message.timestamp);
    } else {
      time.textContent = "—";
    }
    meta.append(time);
    if (message.from_me) meta.append(tickIcon());
    return meta;
  }

  function isSystemMessage(message) {
    const type = message.message_type || "";
    return type === "system" || type === "group_event" || type === "reaction";
  }

  function systemChip(message) {
    const chip = document.createElement("div");
    chip.className = "system-chip";
    const text = message.text || message.media_title || message.message_type.replace("_", " ");
    chip.textContent = message.message_type === "reaction"
      ? `${senderLabel(message)} reacted ${text}`
      : text;
    return chip;
  }

  function senderKey(message) {
    return message.from_me ? "me" : (message.sender_jid || message.sender_name || "unknown");
  }

  // source_pk is the unique archive row id; message ids can be empty or
  // repeated across senders, so they only serve as a fallback key.
  function messageKey(message) {
    return message.source_pk > 0 ? `pk:${message.source_pk}` : `id:${message.message_id}`;
  }

  function bubbleRow(message, runFirst, isGroupChat) {
    const row = document.createElement("div");
    row.className = "bubble-row";
    if (message.from_me) row.classList.add("mine");
    if (runFirst) row.classList.add("run-first");

    if (isGroupChat && !message.from_me) {
      const gutter = document.createElement("span");
      gutter.className = "bubble-gutter";
      if (runFirst) {
        gutter.append(avatarNode(message.sender_jid || message.sender_name, message.sender_name, "", "s"));
      }
      row.append(gutter);
    }

    const bubble = document.createElement("div");
    bubble.className = message.from_me ? "bubble" : "bubble theirs";
    if (runFirst) bubble.append(tailFor(message.from_me));

    if (runFirst && isGroupChat && !message.from_me) {
      const sender = document.createElement("span");
      sender.className = `sender-name sc-${hashCode(senderKey(message)) % 8}`;
      sender.textContent = senderLabel(message);
      bubble.append(sender);
    }

    if (message.media_type || message.media_title || (message.message_type && message.message_type !== "text" && message.message_type !== "link")) {
      bubble.append(mediaCard(message));
    }

    const body = document.createElement("span");
    body.className = "bubble-body";
    if (message.text) {
      if (isJumboEmoji(message.text)) {
        bubble.classList.add("emoji-jumbo");
        body.textContent = message.text;
      } else {
        body.append(renderRichText(message.text));
      }
    }
    bubble.append(body);
    bubble.append(bubbleMeta(message));
    row.append(bubble);
    return row;
  }

  function renderMessages() {
    const chat = state.selectedChat;
    const isGroupChat = chat?.kind === "group";
    elements.messageList.replaceChildren();

    if (chat && state.messages.length < (chat.message_count || 0)) {
      const older = document.createElement("button");
      older.type = "button";
      older.className = "load-older";
      older.id = "load-older";
      older.textContent = `Load older messages (${formatCount(chat.message_count - state.messages.length)} more)`;
      older.addEventListener("click", loadOlder);
      elements.messageList.append(older);
    }

    if (!state.messages.length) {
      const empty = document.createElement("div");
      empty.className = "empty-state";
      empty.append(icon("empty"));
      const text = document.createElement("p");
      text.textContent = "No messages in this conversation.";
      empty.append(text);
      elements.messageList.append(empty);
      return;
    }

    let group = null;
    let prevMessage = null;
    let prevDate = null;

    for (const message of state.messages) {
      const date = parseDate(message.timestamp);
      if (!group || !prevDate !== !date || (date && prevDate && !sameDay(date, prevDate))) {
        group = document.createElement("div");
        group.className = "day-group";
        const chipEl = document.createElement("div");
        chipEl.className = "day-chip";
        chipEl.textContent = formatDayLabel(date);
        group.append(chipEl);
        elements.messageList.append(group);
        prevMessage = null;
      }
      prevDate = date;

      if (isSystemMessage(message)) {
        group.append(systemChip(message));
        prevMessage = null;
        continue;
      }

      let runFirst = true;
      if (prevMessage && senderKey(prevMessage) === senderKey(message)) {
        const prevTime = parseDate(prevMessage.timestamp)?.getTime() ?? 0;
        const thisTime = date?.getTime() ?? 0;
        runFirst = Math.abs(thisTime - prevTime) > RUN_GAP_MS;
      }
      group.append(bubbleRow(message, runFirst, isGroupChat));
      prevMessage = message;
    }
  }

  // ---------- Views ----------

  function showChatView() {
    elements.chatIntro.hidden = true;
    elements.chatView.hidden = false;
    elements.app.classList.add("chat-open");
  }

  function showIntro() {
    elements.chatView.hidden = true;
    elements.chatIntro.hidden = false;
    elements.app.classList.remove("chat-open");
    document.title = "wacrawl — WhatsApp archive";
  }

  function renderLoading(label) {
    elements.messageList.replaceChildren();
    const loading = document.createElement("div");
    loading.className = "loading-state";
    loading.append(icon("spinner"));
    const text = document.createElement("p");
    text.textContent = label;
    loading.append(text);
    elements.messageList.append(loading);
  }

  function setChatHeader(chat) {
    elements.chatAvatar.replaceChildren(avatarNode(chat.jid, chat.name, chat.kind, "m"));
    elements.chatTitle.textContent = chatName(chat);
    const parts = [];
    if (chat.kind === "group") parts.push("Group");
    else if (chat.kind === "newsletter") parts.push("Newsletter");
    else if (chat.kind === "status") parts.push("Status updates");
    else parts.push(prettyJid(chat.jid));
    if (chat.message_count) parts.push(`${formatCount(chat.message_count)} archived message${chat.message_count === 1 ? "" : "s"}`);
    elements.chatSubtitle.textContent = parts.join(" · ");
    elements.chatFlag.hidden = !chat.archived;
    elements.chatFlag.textContent = "archived";
    document.title = `${chatName(chat)} — wacrawl`;
  }

  function setSearchHeader(query, count) {
    const avatar = document.createElement("span");
    avatar.className = "avatar av-0 size-m";
    avatar.setAttribute("aria-hidden", "true");
    avatar.append(icon("search"));
    elements.chatAvatar.replaceChildren(avatar);
    elements.chatTitle.textContent = `Results for “${query}”`;
    elements.chatSubtitle.textContent = count === null
      ? "Searching message text, chats, senders, and media titles…"
      : `${formatCount(count)} match${count === 1 ? "" : "es"} across the archive`;
    elements.chatFlag.hidden = true;
    document.title = `Search: ${query} — wacrawl`;
  }

  async function selectChat(chat, keepSearchText) {
    const request = ++state.viewRequest;
    state.searching = false;
    state.selectedChat = chat;
    state.messages = [];
    state.seenMessageIds = new Set();
    if (!keepSearchText) {
      elements.searchInput.value = "";
      elements.searchClear.hidden = true;
    }
    renderChats();
    showChatView();
    setChatHeader(chat);
    renderLoading("Decrypting local archive…");
    try {
      const messages = await api(`/api/messages?chat=${encodeURIComponent(chat.jid)}&limit=${FETCH_LIMIT}`);
      if (request !== state.viewRequest) return;
      state.messages = messages;
      state.seenMessageIds = new Set(messages.map(messageKey));
      renderMessages();
      elements.messageScroll.scrollTop = elements.messageScroll.scrollHeight;
    } catch (error) {
      if (request !== state.viewRequest) return;
      showToast(error.message);
      state.messages = [];
      renderMessages();
    }
  }

  async function loadOlder() {
    const chat = state.selectedChat;
    if (!chat || !state.messages.length) return;
    const button = document.querySelector("#load-older");
    if (button) {
      button.disabled = true;
      button.textContent = "Loading…";
    }
    const request = state.viewRequest;
    const oldestMessage = state.messages[0];
    const oldest = parseDate(oldestMessage.timestamp);
    const before = oldest ? Math.floor(oldest.getTime() / 1000) : 0;
    // source_pk breaks ties inside a single second so paging always advances.
    const cursorPK = Number(oldestMessage.source_pk || 0);
    const cursor = cursorPK > 0 ? `&before=${before}&before_pk=${cursorPK}` : `&before=${before}`;
    try {
      const older = await api(`/api/messages?chat=${encodeURIComponent(chat.jid)}&limit=${FETCH_LIMIT}${cursor}`);
      if (request !== state.viewRequest) return;
      const fresh = older.filter((m) => !state.seenMessageIds.has(messageKey(m)));
      if (!fresh.length) {
        showToast("You have reached the beginning of this archive.");
        if (button) button.remove();
        return;
      }
      fresh.forEach((m) => state.seenMessageIds.add(messageKey(m)));
      state.messages = fresh.concat(state.messages);
      const scroller = elements.messageScroll;
      const previousHeight = scroller.scrollHeight;
      const previousTop = scroller.scrollTop;
      renderMessages();
      scroller.scrollTop = scroller.scrollHeight - previousHeight + previousTop;
    } catch (error) {
      if (request !== state.viewRequest) return;
      showToast(error.message);
      renderMessages();
    }
  }

  function chatForJid(jid, fallbackName) {
    return state.chats.find((chat) => chat.jid === jid) || { jid, name: fallbackName || "", kind: jid.endsWith("@g.us") ? "group" : "dm" };
  }

  function renderSearchResults(query, messages) {
    elements.messageList.replaceChildren();
    const note = document.createElement("div");
    note.className = "results-note";
    note.textContent = messages.length
      ? `${formatCount(messages.length)} match${messages.length === 1 ? "" : "es"} — newest ranked by relevance`
      : "No matches in this archive.";
    elements.messageList.append(note);

    messages.forEach((message, i) => {
      const card = document.createElement("button");
      card.type = "button";
      card.className = "result-card";
      card.style.setProperty("--i", Math.min(i, 20));

      const head = document.createElement("span");
      head.className = "result-head";
      const chat = chatForJid(message.chat_jid, message.chat_name);
      head.append(avatarNode(chat.jid, chat.name || message.chat_name, chat.kind, "s"));
      const chatLabel = document.createElement("span");
      chatLabel.className = "result-chat";
      chatLabel.textContent = message.chat_name || chatName(chat);
      const when = document.createElement("span");
      when.className = "result-when";
      when.textContent = formatStamp(message.timestamp);
      head.append(chatLabel, when);

      const sender = document.createElement("span");
      sender.className = "result-sender";
      sender.textContent = senderLabel(message);

      const snippet = document.createElement("span");
      snippet.className = "result-snippet";
      if (message.snippet) {
        renderSnippet(snippet, message.snippet);
      } else {
        snippet.textContent = message.text || message.media_title || "";
      }

      card.append(head, sender, snippet);
      card.addEventListener("click", () => selectChat(chat, true));
      elements.messageList.append(card);
    });
    elements.messageScroll.scrollTop = 0;
  }

  async function runSearch(query) {
    const request = ++state.viewRequest;
    state.searching = true;
    state.selectedChat = null;
    renderChats();
    showChatView();
    setSearchHeader(query, null);
    renderLoading("Searching the archive…");
    try {
      const messages = await api(`/api/search?q=${encodeURIComponent(query)}&limit=${FETCH_LIMIT}`);
      if (request !== state.viewRequest) return;
      setSearchHeader(query, messages.length);
      renderSearchResults(query, messages);
    } catch (error) {
      if (request !== state.viewRequest) return;
      showToast(error.message);
      setSearchHeader(query, 0);
      renderSearchResults(query, []);
    }
  }

  // ---------- Load / refresh ----------

  async function load() {
    const request = ++state.viewRequest;
    elements.refresh.disabled = true;
    try {
      const [status, chats] = await Promise.all([api("/api/status"), api(`/api/chats?limit=500`)]);
      if (request !== state.viewRequest) return;
      renderStatus(status);
      state.chats = chats;
      renderChats();
      if (state.searching) {
        const query = elements.searchInput.value.trim();
        if (query) await runSearch(query);
      } else if (state.selectedChat) {
        const current = chats.find((chat) => chat.jid === state.selectedChat.jid);
        if (current) await selectChat(current, true);
        else showIntro();
      }
    } catch (error) {
      showToast(error.message);
    } finally {
      elements.refresh.disabled = false;
    }
  }

  // ---------- Events ----------

  elements.searchForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const query = elements.searchInput.value.trim();
    if (!query) {
      showToast("Type something to search the archive.");
      return;
    }
    runSearch(query);
  });

  elements.searchInput.addEventListener("input", () => {
    elements.searchClear.hidden = elements.searchInput.value === "";
    renderChats();
  });

  elements.searchClear.addEventListener("click", () => {
    elements.searchInput.value = "";
    elements.searchClear.hidden = true;
    if (state.searching) {
      state.searching = false;
      if (state.selectedChat) selectChat(state.selectedChat, false);
      else showIntro();
    }
    renderChats();
    elements.searchInput.focus();
  });

  elements.filterChips.addEventListener("click", (event) => {
    const chip = event.target.closest("[data-filter]");
    if (!chip) return;
    state.filter = chip.dataset.filter;
    elements.filterChips.querySelectorAll(".chip").forEach((node) => {
      node.classList.toggle("active", node === chip);
    });
    renderChats();
  });

  elements.statsToggle.addEventListener("click", () => {
    const open = elements.statsPanel.hidden;
    elements.statsPanel.hidden = !open;
    elements.statsToggle.setAttribute("aria-expanded", String(open));
    elements.statsToggle.classList.toggle("attention", open);
  });

  elements.backButton.addEventListener("click", () => {
    state.viewRequest += 1;
    state.searching = false;
    state.selectedChat = null;
    renderChats();
    showIntro();
  });

  elements.refresh.addEventListener("click", () => load());

  document.addEventListener("keydown", (event) => {
    const typing = event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement;
    if (event.key === "/" && !typing) {
      event.preventDefault();
      elements.searchInput.focus();
      elements.searchInput.select();
    }
    if (event.key === "Escape") {
      if (typing) {
        event.target.blur();
        return;
      }
      if (state.searching || elements.app.classList.contains("chat-open")) {
        elements.backButton.click();
      }
    }
  });

  load();
})();
