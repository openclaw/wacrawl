(() => {
  "use strict";

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
    archiveRange: document.querySelector("#archive-range"),
    chatCount: document.querySelector("#chat-count"),
    chatList: document.querySelector("#chat-list"),
    clearSearch: document.querySelector("#clear-search"),
    messageList: document.querySelector("#message-list"),
    messagePanel: document.querySelector(".message-panel"),
    metrics: document.querySelector("#metrics"),
    panelKicker: document.querySelector("#panel-kicker"),
    panelSubtitle: document.querySelector("#panel-subtitle"),
    panelTitle: document.querySelector("#panel-title"),
    refresh: document.querySelector("#refresh-button"),
    searchForm: document.querySelector("#search-form"),
    searchInput: document.querySelector("#search-input"),
    syncNote: document.querySelector("#sync-note"),
    toast: document.querySelector("#toast"),
  };

  const state = {
    chats: [],
    selectedChat: "",
    searching: false,
    viewRequest: 0,
  };

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

  function formatCount(value) {
    return new Intl.NumberFormat().format(Number(value || 0));
  }

  function formatDay(value) {
    if (!value || value.startsWith("0001-")) return "No date";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "No date";
    return new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      year: date.getFullYear() === new Date().getFullYear() ? undefined : "numeric",
    }).format(date);
  }

  function formatTime(value) {
    if (!value || value.startsWith("0001-")) return "Unknown time";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "Unknown time";
    return new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
    }).format(date);
  }

  function initials(value) {
    const words = (value || "?").trim().split(/\s+/).filter(Boolean);
    return words.slice(0, 2).map((word) => word[0]).join("") || "?";
  }

  function chatName(chat) {
    return chat.name || chat.jid || "Untitled chat";
  }

  function showToast(message) {
    elements.toast.textContent = message;
    elements.toast.hidden = false;
    window.clearTimeout(showToast.timer);
    showToast.timer = window.setTimeout(() => {
      elements.toast.hidden = true;
    }, 4800);
  }

  function clear(node) {
    node.replaceChildren();
  }

  function renderStatus(status) {
    const values = [status.messages, status.chats, status.contacts, status.media_messages];
    elements.metrics.querySelectorAll("dd").forEach((node, index) => {
      node.textContent = formatCount(values[index]);
    });
    elements.archiveRange.textContent = status.messages
      ? `${formatDay(status.oldest_message)} — ${formatDay(status.newest_message)}`
      : "Archive is ready for its first sync.";
    elements.syncNote.textContent = status.last_import_at && !status.last_import_at.startsWith("0001-")
      ? `Archive snapshot · ${formatTime(status.last_import_at)}`
      : "Archive snapshot · not imported yet";
  }

  function renderChats(chats) {
    clear(elements.chatList);
    elements.chatCount.textContent = formatCount(chats.length);
    if (!chats.length) {
      const empty = document.createElement("div");
      empty.className = "empty-state";
      const text = document.createElement("p");
      text.textContent = "No chats in this archive.";
      empty.append(text);
      elements.chatList.append(empty);
      return;
    }

    chats.forEach((chat) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "chat-row";
      button.dataset.jid = chat.jid;
      button.title = chatName(chat);
      if (chat.jid === state.selectedChat && !state.searching) button.classList.add("active");

      const avatar = document.createElement("span");
      avatar.className = "avatar";
      avatar.textContent = initials(chatName(chat));

      const copy = document.createElement("span");
      copy.className = "chat-copy";
      const name = document.createElement("span");
      name.className = "chat-name";
      name.textContent = chatName(chat);
      const meta = document.createElement("span");
      meta.className = "chat-meta";
      meta.textContent = `${chat.kind || "chat"} · ${formatCount(chat.message_count)} notes · ${formatDay(chat.last_message_at)}`;
      copy.append(name, meta);

      button.append(avatar, copy);
      if (chat.unread_count > 0) {
        const unread = document.createElement("span");
        unread.className = "unread-dot";
        unread.textContent = formatCount(chat.unread_count);
        unread.setAttribute("aria-label", `${chat.unread_count} unread`);
        button.append(unread);
      }
      button.addEventListener("click", () => selectChat(chat));
      elements.chatList.append(button);
    });
  }

  function renderLoading(label) {
    clear(elements.messageList);
    const loading = document.createElement("div");
    loading.className = "loading-state";
    const mark = document.createElement("span");
    mark.setAttribute("aria-hidden", "true");
    mark.textContent = "◌";
    const text = document.createElement("p");
    text.textContent = label;
    loading.append(mark, text);
    elements.messageList.append(loading);
  }

  function renderMessages(messages, searchMode = false) {
    clear(elements.messageList);
    if (!messages.length) {
      const empty = document.createElement("div");
      empty.className = "empty-state";
      const mark = document.createElement("span");
      mark.setAttribute("aria-hidden", "true");
      mark.textContent = "◎";
      const text = document.createElement("p");
      text.textContent = searchMode ? "No matching field notes." : "No messages in this thread.";
      empty.append(mark, text);
      elements.messageList.append(empty);
      return;
    }

    messages.forEach((message) => {
      const card = document.createElement("article");
      card.className = "message-card";
      if (message.from_me) card.classList.add("mine");
      if (searchMode) card.classList.add("search-hit");
      const meta = document.createElement("div");
      meta.className = "message-meta";
      const sender = document.createElement("span");
      sender.textContent = message.from_me
        ? "You"
        : (message.sender_name || message.sender_jid || "Unknown sender");
      const time = document.createElement("time");
      time.dateTime = message.timestamp || "";
      time.textContent = formatTime(message.timestamp);
      meta.append(sender, time);

      const body = document.createElement("p");
      body.className = "message-body";
      body.textContent = message.text || message.snippet || message.media_title || "No text content";
      card.append(meta, body);

      if (message.media_type || message.media_title) {
        const media = document.createElement("span");
        media.className = "message-media";
        media.textContent = `Attachment · ${message.media_type || message.media_title}`;
        card.append(media);
      }
      if (searchMode && message.chat_name) {
        const thread = document.createElement("span");
        thread.className = "message-media";
        thread.textContent = `Thread · ${message.chat_name}`;
        card.append(thread);
      }
      elements.messageList.append(card);
    });
    elements.messageList.scrollTop = searchMode ? 0 : elements.messageList.scrollHeight;
  }

  async function selectChat(chat) {
    const request = ++state.viewRequest;
    state.searching = false;
    state.selectedChat = chat.jid;
    elements.clearSearch.hidden = true;
    elements.panelKicker.textContent = chat.kind === "group" ? "GROUP THREAD" : "DIRECT THREAD";
    elements.panelTitle.textContent = chatName(chat);
    elements.panelSubtitle.textContent = `${chat.jid} · ${formatCount(chat.message_count)} archived messages`;
    renderChats(state.chats);
    renderLoading("Reading thread…");
    elements.messagePanel.setAttribute("aria-busy", "true");
    try {
      const messages = await api(`/api/messages?chat=${encodeURIComponent(chat.jid)}&limit=150`);
      if (request !== state.viewRequest) return;
      renderMessages(messages);
    } catch (error) {
      if (request !== state.viewRequest) return;
      showToast(error.message);
      renderMessages([]);
    } finally {
      if (request === state.viewRequest) {
        elements.messagePanel.setAttribute("aria-busy", "false");
      }
    }
  }

  async function runSearch(query) {
    const request = ++state.viewRequest;
    state.searching = true;
    elements.clearSearch.hidden = false;
    elements.panelKicker.textContent = "FULL-TEXT SEARCH";
    elements.panelTitle.textContent = `“${query}”`;
    elements.panelSubtitle.textContent = "Matches across message text, chats, senders, and media titles.";
    renderChats(state.chats);
    renderLoading("Searching field notes…");
    elements.messagePanel.setAttribute("aria-busy", "true");
    try {
      const messages = await api(`/api/search?q=${encodeURIComponent(query)}&limit=150`);
      if (request !== state.viewRequest) return;
      elements.panelSubtitle.textContent = `${formatCount(messages.length)} matches across the local archive.`;
      renderMessages(messages, true);
    } catch (error) {
      if (request !== state.viewRequest) return;
      showToast(error.message);
      renderMessages([], true);
    } finally {
      if (request === state.viewRequest) {
        elements.messagePanel.setAttribute("aria-busy", "false");
      }
    }
  }

  async function reloadCurrentView(chats) {
    if (state.searching) {
      const query = elements.searchInput.value.trim();
      if (query) {
        await runSearch(query);
        return;
      }
    }
    const chat = chats.find((item) => item.jid === state.selectedChat) || chats[0];
    if (chat) await selectChat(chat);
  }

  async function load() {
    const request = ++state.viewRequest;
    const interruptedLoadingView = elements.messagePanel.getAttribute("aria-busy") === "true";
    elements.refresh.disabled = true;
    elements.messagePanel.setAttribute("aria-busy", "true");
    try {
      const [status, chats] = await Promise.all([api("/api/status"), api("/api/chats?limit=200")]);
      if (request !== state.viewRequest) return;
      renderStatus(status);
      state.chats = chats;
      renderChats(chats);
      await reloadCurrentView(chats);
    } catch (error) {
      if (request !== state.viewRequest) return;
      showToast(error.message);
      if (interruptedLoadingView) await reloadCurrentView(state.chats);
    } finally {
      elements.refresh.disabled = false;
      if (request === state.viewRequest) {
        elements.messagePanel.setAttribute("aria-busy", "false");
      }
    }
  }

  elements.searchForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const query = elements.searchInput.value.trim();
    if (!query) {
      showToast("Enter a search term first.");
      return;
    }
    runSearch(query);
  });

  elements.clearSearch.addEventListener("click", () => {
    elements.searchInput.value = "";
    state.searching = false;
    elements.clearSearch.hidden = true;
    const chat = state.chats.find((item) => item.jid === state.selectedChat) || state.chats[0];
    if (chat) selectChat(chat);
  });

  elements.refresh.addEventListener("click", () => load());
  load();
})();
