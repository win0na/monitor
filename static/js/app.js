/**
 * Stream Monitor — Client-Side Application
 *
 * Polls the /stats endpoint every 250ms and updates the dashboard UI.
 * Features:
 *   - Per-digit slot-machine animation for stat values
 *   - Auto-scrolling live chat feed
 *   - YouTube embed for live stream preview
 *   - Server-lost detection after 3 consecutive fetch failures
 */

/* ── DOM Helpers ───────────────────────────────────────────────────────────── */

/** @param {string} id - Element ID to look up */
var $ = function(id) { return document.getElementById(id); };

/* ── Constants & State ─────────────────────────────────────────────────────── */

/** Maximum expected bitrate (kb/s) for the bitrate bar scaling */
var MAX_BIT = 56000;

/** Total number of chat messages seen (monotonic, matches server chat_total) */
var lastChatTotal = 0;

/** Whether the user has manually scrolled the chat feed away from the bottom */
var userScrolled = false;

/** Reference to the chat feed container element */
var chatFeed = $('chat-feed');

/** Consecutive fetch failure counter for server-lost detection */
var _failCount = 0;

/** Whether the UI is currently in server-lost state */
var _serverLost = false;

/** Server boot timestamp — changes when the server restarts */
var _bootTime = null;



/* ══════════════════════════════════════════════════════════════════════════════
   Per-Digit Slot Machine Animation
   ─────────────────────────────────
   Each .sv element holds .dslot spans (one per character).
   Fixed-height .dslot means .sv never changes height → zero layout reflow.
   Only characters that actually differ get animated; the animation direction
   (up or down) follows whether the numeric value increased or decreased.
══════════════════════════════════════════════════════════════════════════════ */

/** Tracks the currently displayed string for each element by ID */
var _slotState = {};

/**
 * Parse a display string into a number for comparison.
 * Strips commas and trailing percent signs.
 * @param {string} s - Display string like "1,234" or "42.5%"
 * @returns {number|null}
 */
function _parseNum(s) {
  var n = parseFloat(String(s || '').replace(/,/g, '').replace(/%.*/, ''));
  return isNaN(n) ? null : n;
}

/**
 * Immediately set an element's content to individual character slots
 * (no animation). Used for initial render or when character count changes.
 * @param {HTMLElement} el - The .sv element
 * @param {string} text - New display text
 */
function _staticSlots(el, text) {
  el.innerHTML = '';
  for (var i = 0; i < text.length; i++) {
    var s = document.createElement('span');
    s.className = 'dslot';
    s.textContent = text[i];
    el.appendChild(s);
  }
}

/**
 * Animate individual digit slots from old text to new text.
 * Only characters at positions that differ get the slide animation.
 * @param {HTMLElement} el - The .sv element
 * @param {string} oldText - Previously displayed text
 * @param {string} newText - New text to transition to
 * @param {number} dir - Animation direction: 1 = slide up, -1 = slide down
 */
function _animateSlots(el, oldText, newText, dir) {
  var slots = el.querySelectorAll('.dslot');
  if (slots.length !== newText.length) { _staticSlots(el, newText); return; }

  for (var i = 0; i < newText.length; i++) {
    if (oldText[i] === newText[i]) continue;
    (function(sl, oc, nc, d) {
      var inner = document.createElement('span');
      inner.className = 'dslot-inner';
      var a = document.createElement('span'); a.textContent = oc;
      var b = document.createElement('span'); b.textContent = nc;

      // dir>0 (up): stack [old, new], animate translateY(0 -> -1.1em)
      // dir<0 (down): stack [new, old], animate translateY(-1.1em -> 0)
      if (d > 0) { inner.appendChild(a); inner.appendChild(b); }
      else       { inner.appendChild(b); inner.appendChild(a); inner.style.transform = 'translateY(-1.1em)'; }

      sl.innerHTML = '';
      sl.appendChild(inner);

      requestAnimationFrame(function() {
        requestAnimationFrame(function() {
          inner.style.transition = 'transform 0.45s cubic-bezier(0.22,0.61,0.36,1)';
          inner.style.transform  = d > 0 ? 'translateY(-1.1em)' : 'translateY(0)';
          inner.addEventListener('transitionend', function() { sl.textContent = nc; }, { once: true });
        });
      });
    })(slots[i], oldText[i], newText[i], dir);
  }
}

/**
 * Update a stat value element with slot-machine animation.
 * Automatically determines animation direction from numeric comparison.
 * @param {HTMLElement} el - The .sv element to update
 * @param {string} newText - New display text
 */
function digitSet(el, newText) {
  if (!el) return;
  var key = el.id;
  var oldText = _slotState[key];

  if (oldText === undefined || oldText === null) {
    _staticSlots(el, newText);
    _slotState[key] = newText;
    return;
  }
  if (oldText === newText) return;

  var on = _parseNum(oldText), nn = _parseNum(newText);
  var dir = (on !== null && nn !== null) ? (nn >= on ? 1 : -1) : 1;

  if (oldText.length !== newText.length) _staticSlots(el, newText);
  else _animateSlots(el, oldText, newText, dir);

  _slotState[key] = newText;
}

/**
 * Clear a stat value element back to a static placeholder.
 * @param {HTMLElement} el - The .sv element to clear
 * @param {string} [text] - Placeholder text (defaults to '—')
 */
function digitClear(el, text) {
  if (!el) return;
  _staticSlots(el, text || '\u2014');
  _slotState[el.id] = undefined;
}


/* ══════════════════════════════════════════════════════════════════════════════
   Chat Message Queue
   ──────────────────
   YouTube's innertube API delivers chat messages in batches every ~5 seconds.
   To replicate the smooth, real-time scrolling of YouTube's own live chat,
   messages are queued and rendered one-by-one with staggered timing spread
   evenly across the interval between batches.
══════════════════════════════════════════════════════════════════════════════ */

/** Pending messages waiting to be rendered, oldest first */
var _chatQueue = [];

/** Handle for the staggered render timer (null when idle) */
var _chatTimer = null;

/** Detect when the user scrolls away from the bottom of the chat feed */
chatFeed.addEventListener('scroll', function() {
  userScrolled = chatFeed.scrollHeight - chatFeed.scrollTop - chatFeed.clientHeight >= 40;
});

/** Auto-scroll chat to the bottom unless the user has scrolled up */
function scrollToBottom() {
  if (!userScrolled) chatFeed.scrollTop = chatFeed.scrollHeight;
}

/**
 * Build the message text span, rendering emoji images inline when parts are available.
 * Falls back to plain textContent when no structured parts exist.
 * @param {Object} msg - Chat message object
 * @returns {HTMLSpanElement}
 */
function buildMsgText(msg) {
  var txt = document.createElement('span');
  txt.className = 'msg-text';

  if (!msg.parts || msg.parts.length === 0) {
    txt.textContent = msg.message;
    return txt;
  }

  for (var i = 0; i < msg.parts.length; i++) {
    var p = msg.parts[i];
    if (p.emoji) {
      var img = document.createElement('img');
      img.className = 'chat-emoji';
      img.src = p.emoji;
      img.alt = p.alt || '';
      img.title = p.alt || '';
      img.loading = 'lazy';
      txt.appendChild(img);
    } else if (p.text) {
      txt.appendChild(document.createTextNode(p.text));
    }
  }

  return txt;
}

/**
 * Append a single chat message to the feed DOM immediately.
 * @param {Object} msg - Chat message object
 * @param {string} msg.author - Display name of the message author
 * @param {string} msg.message - Message text content (plain-text fallback)
 * @param {Object[]} [msg.parts] - Structured message parts with emoji image URLs
 * @param {string} msg.role - Author role: "owner", "mod", "member", or "user"
 * @param {string} [msg.time] - Timestamp string (e.g. "14:32")
 */
function appendMsg(msg) {
  var empty = $('chat-empty');
  if (empty) empty.remove();

  var div  = document.createElement('div');  div.className = 'msg';
  var ts   = document.createElement('span'); ts.className  = 'msg-time'; ts.textContent = msg.time || '';
  var auth = document.createElement('span'); auth.className = 'msg-author ' + (msg.role || 'user'); auth.textContent = msg.author;
  div.appendChild(ts);
  div.appendChild(auth);
  div.appendChild(buildMsgText(msg));
  chatFeed.appendChild(div);

  // Cap DOM nodes to prevent memory growth
  while (chatFeed.children.length > 250) chatFeed.removeChild(chatFeed.children[0]);
  scrollToBottom();
}

/**
 * Enqueue a batch of new chat messages for staggered rendering.
 *
 * Messages are spread evenly across a ~5 second window (the typical
 * YouTube poll interval) so they appear to scroll in continuously
 * rather than arriving in a visible burst every few seconds.
 * @param {Object[]} msgs - Array of chat message objects to queue
 */
function enqueueMsgs(msgs) {
  for (var i = 0; i < msgs.length; i++) _chatQueue.push(msgs[i]);
  if (!_chatTimer) _drainQueue();
}

/**
 * Render one message from the queue and schedule the next.
 *
 * The interval between renders is calculated as 4500ms / queue_length,
 * clamped between 50ms (burst of 90+ msgs) and 500ms (trickle of ~9).
 * This creates smooth, continuous scrolling regardless of batch size.
 */
function _drainQueue() {
  if (_chatQueue.length === 0) { _chatTimer = null; return; }

  appendMsg(_chatQueue.shift());

  // Spread remaining messages across ~4.5s (just under the 5s poll)
  var interval = _chatQueue.length > 0
    ? Math.max(50, Math.min(500, 4500 / (_chatQueue.length + 1)))
    : 0;

  if (_chatQueue.length > 0) {
    _chatTimer = setTimeout(_drainQueue, interval);
  } else {
    _chatTimer = null;
  }
}

/**
 * Insert a horizontal reconnect divider into the chat feed.
 * Shows a timestamp indicating when the connection was re-established.
 */
function appendReconnectDivider() {
  var div = document.createElement('div');
  div.className = 'chat-divider';
  var now = new Date();
  var hh = String(now.getHours()).padStart(2, '0');
  var mm = String(now.getMinutes()).padStart(2, '0');
  div.textContent = 'reconnected ' + hh + ':' + mm;
  chatFeed.appendChild(div);
  scrollToBottom();
}


/* ══════════════════════════════════════════════════════════════════════════════
   Card Health Helper
══════════════════════════════════════════════════════════════════════════════ */

/**
 * Set warning/critical state on a stat card and its value element.
 * @param {string} cid - Card container element ID
 * @param {string} vid - Value element ID
 * @param {string} state - Health state: "warn", "crit", or "" (normal)
 */
function setCard(cid, vid, state) {
  var c = $(cid), v = $(vid);
  if (!c || !v) return;
  c.classList.remove('warn-state', 'crit-state');
  v.classList.remove('warn', 'crit');
  if (state === 'warn') { c.classList.add('warn-state'); v.classList.add('warn'); }
  if (state === 'crit') { c.classList.add('crit-state'); v.classList.add('crit'); }
}


/* ══════════════════════════════════════════════════════════════════════════════
   Server-Lost State
   ─────────────────
   After 3 consecutive fetch failures the UI enters "server lost" mode:
   all stat cards are dimmed, header shows "SERVER LOST", values are cleared.
══════════════════════════════════════════════════════════════════════════════ */

var OBS_CARDS = ['c-bit', 'c-cpu', 'c-gpu', 'c-fps'];
var YT_CARDS  = ['c-viewers', 'c-dur'];

/** Enter server-lost state: dim all cards and clear values */
function showServerLost() {
  // Header indicators
  $('obs-dot').className = 'dot'; $('obs-status').className = 'badge-txt';
  $('yt-dot').className  = 'dot'; $('yt-status').className  = 'badge-txt';
  $('obs-status').dataset.label = 'SERVER LOST'; $('yt-status').dataset.label = '';

  // Dim all stat cards and hide live bar
  var statsEl = document.querySelector('.stats');
  statsEl.classList.add('server-lost');
  statsEl.classList.remove('yt-live');
  OBS_CARDS.concat(YT_CARDS).forEach(function(id) {
    var el = $(id); if (el) el.classList.add('server-lost');
  });

  // Clear OBS values
  ['v-bit', 'v-cpu', 'v-gpu', 'v-fps'].forEach(function(id) {
    digitClear($(id), '\u2014');
  });
  $('c-fps').querySelector('.sl').dataset.drop = '';
  $('bbar-wrap').style.display = 'none';
  $('dur-h').style.display = 'none'; $('dur-hu').style.display = 'none';
  digitClear($('dur-m'), '\u2014'); digitClear($('dur-s'), '');

  // Clear YT values
  digitClear($('v-viewers'), '\u2014');

  // Dim chat and show reconnect overlay
  document.querySelector('.chat-wrap').classList.add('server-lost');
}

/** Exit server-lost state: remove dimming from all cards and chat overlay */
function clearServerLost() {
  document.querySelector('.stats').classList.remove('server-lost');
  OBS_CARDS.concat(YT_CARDS).forEach(function(id) {
    var el = $(id); if (el) el.classList.remove('server-lost');
  });
  document.querySelector('.chat-wrap').classList.remove('server-lost');
}


/* ══════════════════════════════════════════════════════════════════════════════
   Update Functions — Apply Server Data to the UI
══════════════════════════════════════════════════════════════════════════════ */

/**
 * Update all OBS-related stat cards from server data.
 * @param {Object} obs - OBS state from /stats response
 */
function updateOBS(obs) {
  var s  = obs.stats  || {};
  var st = obs.stream || {};

  if (!obs.connected) {
    $('obs-dot').className    = 'dot';
    $('obs-status').className = 'badge-txt';
    $('obs-status').dataset.label = 'OBS offline';
    return;
  }

  var live = !!st.outputActive;
  $('obs-dot').className    = 'dot obs-on';
  $('obs-status').className = 'badge-txt obs-on';
  $('obs-status').dataset.label = live ? 'OBS LIVE' : 'OBS on';

  // Duration
  if (live && st.outputDuration !== undefined) {
    var ms = st.outputDuration;
    var th = Math.floor(ms / 3600000);
    var tm = Math.floor((ms % 3600000) / 60000);
    var ts = Math.floor((ms % 60000) / 1000);
    var hEl = $('dur-h'), huEl = $('dur-hu');
    if (th > 0) {
      hEl.style.display = ''; huEl.style.display = '';
      digitSet(hEl, String(th));
    } else {
      hEl.style.display = 'none'; huEl.style.display = 'none';
    }
    digitSet($('dur-m'), String(tm));
    digitSet($('dur-s'), String(ts));
  } else if (!live) {
    $('dur-h').style.display = 'none'; $('dur-hu').style.display = 'none';
    digitClear($('dur-m'), '\u2014'); digitClear($('dur-s'), '');
  }

  // Bitrate
  var kbps = (obs.kbps !== null && obs.kbps !== undefined) ? obs.kbps : null;
  if (kbps !== null && live) {
    digitSet($('v-bit'), kbps.toLocaleString());
    $('bbar-wrap').style.display = 'block';
    $('bbar').style.width = Math.min(kbps / MAX_BIT * 100, 100) + '%';

    var barColor, cardState;
    if      (kbps < 13000) { barColor = 'var(--crit)'; cardState = 'crit'; }
    else if (kbps < 35000) { barColor = 'var(--warn)'; cardState = 'warn'; }
    else                   { barColor = 'var(--obs)';  cardState = ''; }

    $('bbar').style.background = barColor;
    var vBit = $('v-bit');
    vBit.classList.remove('warn', 'crit');
    if (cardState) vBit.classList.add(cardState);
  } else {
    digitClear($('v-bit'), '\u2014');
    $('v-bit').classList.remove('warn', 'crit');
    $('bbar-wrap').style.display = 'none';
  }

  // CPU
  var cpu = s.cpuUsage !== undefined ? s.cpuUsage.toFixed(1) : null;
  digitSet($('v-cpu'), cpu !== null ? cpu : '\u2014');
  if (cpu !== null) setCard('c-cpu', 'v-cpu', cpu > 80 ? 'crit' : cpu > 60 ? 'warn' : '');

  // FPS + frame drop percentage
  var fps = s.activeFps !== undefined ? s.activeFps.toFixed(1) : null;
  digitSet($('v-fps'), fps !== null ? fps : '\u2014');
  if (fps !== null) {
    var diff = (s.nominalFps || 60) - parseFloat(fps);
    setCard('c-fps', 'v-fps', diff > 5 ? 'crit' : diff > 1 ? 'warn' : '');
  }
  var dropped = live ? (st.outputSkippedFrames || 0) : (s.renderSkippedFrames || 0);
  var total   = live ? (st.outputTotalFrames   || 0) : (s.renderTotalFrames   || 0);
  $('c-fps').querySelector('.sl').dataset.drop = (total > 0 ? ((dropped / total) * 100).toFixed(2) : '0.00') + '% drop';
}

/**
 * Update the GPU stat card from server data.
 * Shows "n/a" in the label when GPU reading is unavailable.
 * @param {Object} g - GPU state from /stats response
 */
function updateGPU(g) {
  var pct = g && g.pct !== null ? g.pct : null;
  var gpuLabel = $('c-gpu').querySelector('.sl');
  if (pct !== null) {
    digitSet($('v-gpu'), pct.toFixed(1));
    setCard('c-gpu', 'v-gpu', pct > 90 ? 'crit' : pct > 70 ? 'warn' : '');
    gpuLabel.classList.remove('gpu-na');
  } else {
    digitSet($('v-gpu'), '\u2014');
    gpuLabel.classList.add('gpu-na');
  }
}

/** Currently displayed video ID (prevents redundant iframe reloads) */
var currentPreviewId = null;

/**
 * Update the stream preview embed. Only reloads the iframe when the
 * video ID actually changes.
 * @param {string|null} videoId - YouTube video ID, or null if offline
 */
function updatePreview(videoId) {
  if (videoId === currentPreviewId) return;
  currentPreviewId = videoId;

  var preview = $('preview');
  var old = preview.querySelector('iframe');
  if (old) old.remove();
  var offline = $('preview-offline');

  if (videoId) {
    offline.style.display = 'none';
    var iframe = document.createElement('iframe');
    iframe.src = 'https://www.youtube.com/embed/' + videoId + '?autoplay=1&mute=1&controls=1&modestbranding=1&rel=0';
    iframe.allow = 'autoplay; encrypted-media; picture-in-picture';
    iframe.allowFullscreen = true;
    preview.appendChild(iframe);
  } else {
    offline.style.display = 'flex';
  }
}

/**
 * Update all YouTube-related UI: status badge, viewers, preview, and chat.
 * @param {Object} yt - YouTube state from /stats response
 */
function updateYT(yt) {
  if (yt.error && !yt.connected) {
    $('yt-dot').className = 'dot'; $('yt-status').className = 'badge-txt';
    $('yt-status').dataset.label = 'YT offline';
  } else if (yt.connected) {
    $('yt-dot').className    = 'dot live';
    $('yt-status').className = 'badge-txt yt-live';
    $('yt-status').dataset.label = 'YT LIVE';
  }

  // Toggle the red top bar on the stats grid
  var statsEl = document.querySelector('.stats');
  if (yt.connected) { statsEl.classList.add('yt-live'); }
  else              { statsEl.classList.remove('yt-live'); }

  $('chat-err').textContent   = yt.error || '';
  $('chat-err').style.display = yt.error ? 'block' : 'none';

  var v = yt.viewers !== null && yt.viewers !== undefined
    ? parseInt(yt.viewers).toLocaleString()
    : '\u2014';
  digitSet($('v-viewers'), v);

  updatePreview(yt.video_id || null);

  // Incremental chat rendering — detect new messages via monotonic total.
  // On first load (lastChatTotal === 0), render all existing messages
  // immediately so the backlog appears instantly. Only genuinely new
  // messages that arrive after initial load are stagger-rendered.
  var msgs = yt.chat || [];
  var total = yt.chat_total || 0;
  $('chat-count').textContent = total > 0 ? total + ' msgs' : '';

  // Server restarted — its counter reset. If the feed already has messages
  // (from before the restart), keep lastChatTotal high so the re-scraped
  // backlog is ignored. Only sync down when the feed is truly empty.
  if (total < lastChatTotal) {
    if (chatFeed.querySelectorAll('.msg').length === 0) {
      lastChatTotal = total;
    }
    // else: keep lastChatTotal as-is; total will eventually catch up
  }
  if (total > lastChatTotal) {
    var newCount = total - lastChatTotal;
    var batch = msgs.slice(-newCount);
    if (lastChatTotal === 0 && chatFeed.querySelectorAll('.msg').length === 0) {
      // First load with empty feed — render backlog immediately
      for (var i = 0; i < batch.length; i++) appendMsg(batch[i]);
    } else {
      enqueueMsgs(batch);
    }
    lastChatTotal = total;
  }
}


/* ══════════════════════════════════════════════════════════════════════════════
   Polling Loop
   ────────────
   Fetches /stats every 250ms with a 3-second hard timeout via AbortController.
   After 3 consecutive failures (~750ms) declares server lost immediately.
══════════════════════════════════════════════════════════════════════════════ */

function poll() {
  var ctrl = new AbortController();
  var tid  = setTimeout(function() { ctrl.abort(); }, 3000);

  fetch('/stats', { signal: ctrl.signal })
    .then(function(r) { clearTimeout(tid); return r.json(); })
    .then(function(data) {
      // Detect server restart — silently hot-reload CSS without a full
      // page reload so the chat feed and UI state are preserved.
      var didReconnect = false;
      if (data._boot) {
        if (_bootTime && _bootTime !== data._boot) {
          _seamlessReload();
          return; // Stop processing — page is about to reload
        }
        _bootTime = data._boot;
      }
      if (_serverLost) {
        _serverLost = false;
        clearServerLost();
        didReconnect = true;
      }
      if (didReconnect && chatFeed.children.length > 0) {
        appendReconnectDivider();
      }
      _failCount = 0;
      updateOBS(data.obs || {});
      updateGPU(data.gpu || {});
      updateYT(data.yt   || {});
    })
    .catch(function() {
      clearTimeout(tid);
      _failCount++;
      if (_failCount >= 3 && !_serverLost) { _serverLost = true; showServerLost(); }
    });
}

/**
 * Perform a seamless full-page reload without visible flash.
 *
 * 1. Saves the chat feed HTML and scroll state to sessionStorage
 * 2. Captures the current page as a full-screen canvas screenshot
 * 3. Overlays the screenshot so the reload is visually invisible
 * 4. Calls location.reload() — the fresh page loads behind the overlay
 * 5. On the new page load, chat is restored and the overlay fades out
 *
 * This gives us a true full reload (fresh HTML, CSS, JS) while looking
 * seamless to the user. The canvas overlay hides the white flash.
 */
function _seamlessReload() {
  // Save chat state for restoration after reload
  sessionStorage.setItem('_chat_html', chatFeed.innerHTML);
  sessionStorage.setItem('_chat_total', String(lastChatTotal));
  sessionStorage.setItem('_chat_scrolled', userScrolled ? '1' : '0');
  sessionStorage.setItem('_reload_pending', '1');

  // Hide current content immediately — the <html style="background:#0a0b0d">
  // inline style ensures the background matches during the reload gap,
  // preventing any visible flash between old and new page.
  document.body.style.visibility = 'hidden';
  location.reload();
}

// Restore chat and remove reload overlay after a seamless reload
(function() {
  var savedHtml = sessionStorage.getItem('_chat_html');
  var pending = sessionStorage.getItem('_reload_pending');

  if (savedHtml && pending) {
    chatFeed.innerHTML = savedHtml;
    lastChatTotal = parseInt(sessionStorage.getItem('_chat_total') || '0', 10);
    var wasScrolled = sessionStorage.getItem('_chat_scrolled') === '1';
    userScrolled = wasScrolled;
    if (!wasScrolled) scrollToBottom();

    // Insert reconnect divider at the boundary
    if (chatFeed.querySelectorAll('.msg').length > 0) {
      appendReconnectDivider();
    }
  }

  // Clean up sessionStorage
  sessionStorage.removeItem('_chat_html');
  sessionStorage.removeItem('_chat_total');
  sessionStorage.removeItem('_chat_scrolled');
  sessionStorage.removeItem('_reload_pending');

  // Fade out the overlay from the previous page (if it exists in the DOM cache)
  // The overlay was added before reload, so it won't persist — but the solid
  // background color transition helps mask any remaining flash.
})();

// Start polling immediately, then every 250ms
poll();
setInterval(poll, 250);
