// Package youtube monitors YouTube live streams for the Stream Monitor.
//
// Discovers live streams by scraping the channel's /live page or monitors
// a specific video ID directly. Polls concurrent viewer counts and streams
// live chat via YouTube's innertube API. No YouTube API key required.
package youtube

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"stream_monitor/internal/state"
)

// ── HTTP headers that bypass YouTube's consent gate ──────────────────────────

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/122.0.0.0 Safari/537.36"

const cookie = "CONSENT=PENDING+999; " +
	"SOCS=CAISNQgDEitib3FfaWRlbnRpdHlmcm9udGVuZHVpc2VydmVyXzIwMjMwODI5LjA3X3AxGgJlbiACGgYIgJnSmgY"

// ytFetch fetches a YouTube page with spoofed headers.
func ytFetch(url string) (string, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cookie", cookie)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	return string(body), resp.Request.URL.String(), nil
}

// ── Input parsing ───────────────────────────────────────────────────────────

var (
	reURLBase     = regexp.MustCompile(`https?://(?:www\.)?(?:youtube\.com|youtu\.be)/`)
	reShortURL    = regexp.MustCompile(`https?://youtu\.be/([A-Za-z0-9_-]{11})`)
	reWatchURL    = regexp.MustCompile(`[?&]v=([A-Za-z0-9_-]{11})`)
	reHandleURL   = regexp.MustCompile(`/@([A-Za-z0-9_.-]+)`)
	reVideoID     = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	reHandleBare  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	reConcurrent  = regexp.MustCompile(`"concurrentViewers"\s*:\s*"(\d+)"`)
	reVideoIDJSON = regexp.MustCompile(`"videoId"\s*:\s*"([A-Za-z0-9_-]{11})"`)
)

// ParseInput parses user input into a (kind, value) tuple.
// Returns ("channel", handle), ("video", videoID), or ("", "").
func ParseInput(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", ""
	}

	if reURLBase.MatchString(text) {
		if m := reShortURL.FindStringSubmatch(text); m != nil {
			return "video", m[1]
		}
		if m := reWatchURL.FindStringSubmatch(text); m != nil {
			return "video", m[1]
		}
		if m := reHandleURL.FindStringSubmatch(text); m != nil {
			return "channel", m[1]
		}
		return "", ""
	}

	if strings.HasPrefix(text, "@") {
		handle := strings.TrimLeft(text, "@")
		handle = strings.TrimSpace(handle)
		if handle != "" {
			return "channel", handle
		}
		return "", ""
	}

	if reVideoID.MatchString(text) {
		return "video", text
	}

	if reHandleBare.MatchString(text) {
		return "channel", text
	}

	return "", ""
}

// ValidateChannel checks that a YouTube channel handle resolves to a real page.
func ValidateChannel(handle string) bool {
	html, _, err := ytFetch(fmt.Sprintf("https://www.youtube.com/@%s", handle))
	if err != nil {
		return false
	}
	lower := strings.ToLower(html)
	return strings.Contains(lower, "/@"+strings.ToLower(handle)) ||
		strings.Contains(html, `"channelId"`) ||
		strings.Contains(html, `"externalId"`)
}

// ValidateVideo checks that a video ID points to a currently-live video.
func ValidateVideo(videoID string) bool {
	html, _, err := ytFetch(fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID))
	if err != nil {
		return false
	}
	if !strings.Contains(html, `"playabilityStatus"`) {
		return false
	}
	return strings.Contains(html, `"isLive":true`) ||
		strings.Contains(html, `"isLiveNow":true`) ||
		strings.Contains(html, `"isLiveBroadcast":true`) ||
		reConcurrent.MatchString(html)
}

// ── Viewer count polling ────────────────────────────────────────────────────

// fetchInnertubeViewers fetches concurrent viewer count via innertube API.
func fetchInnertubeViewers(videoID string) *string {
	payload, _ := json.Marshal(map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB",
				"clientVersion": "2.20240313.05.00",
			},
		},
		"videoId": videoID,
	})

	req, err := http.NewRequest("POST",
		"https://www.youtube.com/youtubei/v1/updated_metadata",
		bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	actions, _ := data["actions"].([]any)
	for _, a := range actions {
		action, _ := a.(map[string]any)
		vca, _ := action["updateViewershipAction"].(map[string]any)
		if vca == nil {
			continue
		}
		vc1, _ := vca["viewCount"].(map[string]any)
		vc2, _ := vc1["videoViewCountRenderer"].(map[string]any)
		vc3, _ := vc2["viewCount"].(map[string]any)

		// Try runs array
		if runs, ok := vc3["runs"].([]any); ok && len(runs) > 0 {
			first, _ := runs[0].(map[string]any)
			text, _ := first["text"].(string)
			digits := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(text), ",", ""), ".", "")
			if isDigits(digits) {
				return &digits
			}
		}

		// Try simpleText
		if simple, ok := vc3["simpleText"].(string); ok {
			reNum := regexp.MustCompile(`^[\d,.\s]+`)
			if m := reNum.FindString(simple); m != "" {
				digits := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(m), ",", ""), ".", "")
				if isDigits(digits) {
					return &digits
				}
			}
		}
	}
	return nil
}

// isDigits returns true if s is a non-empty string of digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// scrapeLiveStatus scrapes /@handle/live for video ID and viewer count.
func scrapeLiveStatus(handle string) (string, *string) {
	html, finalURL, err := ytFetch(fmt.Sprintf("https://www.youtube.com/@%s/live", handle))
	if err != nil {
		return "", nil
	}

	isLive := strings.Contains(html, `"isLive":true`) ||
		strings.Contains(html, `"isLiveNow":true`) ||
		reConcurrent.MatchString(html)
	if !isLive {
		return "", nil
	}

	var videoID string
	if m := reWatchURL.FindStringSubmatch(finalURL); m != nil {
		videoID = m[1]
	} else if m := reVideoIDJSON.FindStringSubmatch(html); m != nil {
		videoID = m[1]
	}
	if videoID == "" {
		return "", nil
	}

	var viewers *string
	if m := reConcurrent.FindStringSubmatch(html); m != nil {
		viewers = &m[1]
	}
	if viewers == nil {
		viewers = fetchInnertubeViewers(videoID)
	}
	return videoID, viewers
}

// pollVideoViewers fetches viewer count for a known video ID.
func pollVideoViewers(videoID string) *string {
	html, _, err := ytFetch(fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID))
	if err != nil {
		return fetchInnertubeViewers(videoID)
	}

	isLive := strings.Contains(html, `"isLive":true`) ||
		strings.Contains(html, `"isLiveNow":true`) ||
		reConcurrent.MatchString(html)
	if !isLive {
		return nil
	}

	if m := reConcurrent.FindStringSubmatch(html); m != nil {
		return &m[1]
	}
	return fetchInnertubeViewers(videoID)
}

// ── Stats polling loops ─────────────────────────────────────────────────────

// RunStats polls YouTube for stream status and viewer count (blocking).
func RunStats(ytInput string, s *state.YTState) {
	kind, value := ParseInput(ytInput)
	if kind == "video" {
		runStatsVideo(value, s)
	} else {
		runStatsChannel(value, s)
	}
}

// runStatsChannel polls a channel's /live page for stream status.
func runStatsChannel(handle string, s *state.YTState) {
	for {
		videoID, viewers := scrapeLiveStatus(handle)

		s.Mu.Lock()
		if videoID == "" {
			errMsg := "No live stream found — waiting..."
			s.Connected = false
			s.Error = &errMsg
			s.VideoID = ""
			s.Viewers = nil
			s.Mu.Unlock()
			time.Sleep(30 * time.Second)
			continue
		}

		if videoID != s.VideoID {
			fmt.Printf("✓ Live video found: %s\n", videoID)
			s.VideoID = videoID
			s.Chat = []state.ChatMessage{}
		}

		s.Connected = true
		s.Error = nil
		if viewers != nil {
			s.Viewers = viewers
		}
		s.Mu.Unlock()

		time.Sleep(30 * time.Second)
	}
}

// runStatsVideo polls a specific video ID for viewer count.
func runStatsVideo(videoID string, s *state.YTState) {
	fmt.Printf("✓ Monitoring video: %s\n", videoID)

	s.Mu.Lock()
	s.VideoID = videoID
	s.Connected = true
	s.Error = nil
	s.Mu.Unlock()

	for {
		viewers := pollVideoViewers(videoID)

		s.Mu.Lock()
		if viewers != nil {
			s.Connected = true
			s.Error = nil
			s.Viewers = viewers
		} else {
			s.Connected = false
			errMsg := "Stream may have ended"
			s.Error = &errMsg
			s.Viewers = nil
		}
		s.Mu.Unlock()

		time.Sleep(30 * time.Second)
	}
}

// ── Live chat ───────────────────────────────────────────────────────────────

var (
	reYTInitialData1 = regexp.MustCompile(`window\["ytInitialData"\]\s*=\s*(\{.+\});\s*</script>`)
	reYTInitialData2 = regexp.MustCompile(`var\s+ytInitialData\s*=\s*(\{.+\});\s*</script>`)
)

// extractMessage extracts plain text and structured parts from a YouTube message runs array.
// Parts preserve emoji image URLs for rich rendering; the plain text is a fallback.
func extractMessage(runs []any) (string, []state.MessagePart) {
	var textBuf []string
	var parts []state.MessagePart

	for _, r := range runs {
		run, _ := r.(map[string]any)
		if text, ok := run["text"].(string); ok {
			textBuf = append(textBuf, text)
			parts = append(parts, state.MessagePart{Text: text})
		} else if emoji, ok := run["emoji"].(map[string]any); ok {
			// Determine display fallback text
			alt := ""
			if shortcuts, ok := emoji["shortcuts"].([]any); ok && len(shortcuts) > 0 {
				if s, ok := shortcuts[0].(string); ok {
					alt = s
				}
			}
			if alt == "" {
				if eid, ok := emoji["emojiId"].(string); ok {
					alt = eid
				} else {
					alt = "⭐"
				}
			}

			// Extract emoji image URL from thumbnails
			var imgURL string
			if image, ok := emoji["image"].(map[string]any); ok {
				if thumbs, ok := image["thumbnails"].([]any); ok && len(thumbs) > 0 {
					// Prefer the last (largest) thumbnail
					if thumb, ok := thumbs[len(thumbs)-1].(map[string]any); ok {
						if u, ok := thumb["url"].(string); ok {
							imgURL = u
						}
					}
				}
			}

			textBuf = append(textBuf, alt)
			if imgURL != "" {
				parts = append(parts, state.MessagePart{Emoji: imgURL, Alt: alt})
			} else {
				parts = append(parts, state.MessagePart{Text: alt})
			}
		}
	}
	return strings.TrimSpace(strings.Join(textBuf, "")), parts
}

// extractRole determines the author role from a chat message renderer's badges.
func extractRole(renderer map[string]any) string {
	badges, _ := renderer["authorBadges"].([]any)
	for _, b := range badges {
		badge, _ := b.(map[string]any)
		br, _ := badge["liveChatAuthorBadgeRenderer"].(map[string]any)
		icon, _ := br["icon"].(map[string]any)
		iconType, _ := icon["iconType"].(string)
		switch iconType {
		case "OWNER":
			return "owner"
		case "MODERATOR":
			return "mod"
		case "MEMBER":
			return "member"
		}
	}
	return "user"
}

// contKeys is the priority order for continuation token extraction.
var contKeys = []string{"timedContinuationData", "invalidationContinuationData", "reloadContinuationData"}

// extractContinuation extracts the continuation token from a continuations array.
func extractContinuation(conts []any) (string, int) {
	for _, c := range conts {
		cont, _ := c.(map[string]any)
		for _, ck := range contKeys {
			cd, _ := cont[ck].(map[string]any)
			if cd == nil {
				continue
			}
			token, _ := cd["continuation"].(string)
			if token == "" {
				continue
			}
			pollMs := 0
			if ms, ok := cd["timeoutMs"].(float64); ok {
				pollMs = int(ms)
			}
			return token, pollMs
		}
	}
	return "", 0
}

// getLiveChatInit fetches the initial live chat page and extracts messages + continuation.
func getLiveChatInit(videoID string) ([]state.ChatMessage, string) {
	html, _, err := ytFetch(fmt.Sprintf("https://www.youtube.com/live_chat?v=%s&is_popout=1", videoID))
	if err != nil {
		return nil, ""
	}

	// Extract ytInitialData
	var jsonStr string
	if m := reYTInitialData1.FindStringSubmatch(html); m != nil {
		jsonStr = m[1]
	} else if m := reYTInitialData2.FindStringSubmatch(html); m != nil {
		jsonStr = m[1]
	}
	if jsonStr == "" {
		return nil, ""
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil, ""
	}

	// Extract continuation token
	var continuation string
	if contents, ok := dig(data, "contents", "liveChatRenderer", "continuations").([]any); ok {
		continuation, _ = extractContinuation(contents)
	}

	// Extract initial messages
	var messages []state.ChatMessage
	if actions, ok := dig(data, "contents", "liveChatRenderer", "actions").([]any); ok {
		for _, a := range actions {
			action, _ := a.(map[string]any)
			item := digItem(action)
			if item == nil {
				continue
			}

			renderer := getChatRenderer(item)
			if renderer == nil {
				continue
			}

			author, _ := dig(renderer, "authorName", "simpleText").(string)
			runs, _ := dig(renderer, "message", "runs").([]any)
			text, parts := extractMessage(runs)
			if text == "" || author == "" {
				continue
			}

			messages = append(messages, state.ChatMessage{
				Author:  author,
				Message: text,
				Parts:   parts,
				Role:    extractRole(renderer),
				Time:    time.Now().Format("15:04:05"),
			})
		}
	}

	return messages, continuation
}

// parseChatResponse parses a get_live_chat innertube response.
func parseChatResponse(data map[string]any) ([]state.ChatMessage, string, int) {
	var nextCont string
	var pollMs int

	if conts, ok := dig(data, "continuationContents", "liveChatContinuation", "continuations").([]any); ok {
		nextCont, pollMs = extractContinuation(conts)
	}

	var messages []state.ChatMessage
	if actions, ok := dig(data, "continuationContents", "liveChatContinuation", "actions").([]any); ok {
		for _, a := range actions {
			action, _ := a.(map[string]any)
			addItem, _ := action["addChatItemAction"].(map[string]any)
			item, _ := addItem["item"].(map[string]any)
			if item == nil {
				continue
			}

			renderer := getChatRenderer(item)
			if renderer == nil {
				continue
			}

			author, _ := dig(renderer, "authorName", "simpleText").(string)
			runs, _ := dig(renderer, "message", "runs").([]any)
			text, parts := extractMessage(runs)
			if text == "" || author == "" {
				continue
			}

			messages = append(messages, state.ChatMessage{
				Author:  author,
				Message: text,
				Parts:   parts,
				Role:    extractRole(renderer),
			})
		}
	}

	return messages, nextCont, pollMs
}

// RunChat streams live chat messages by polling YouTube's innertube API (blocking).
func RunChat(ytInput string, s *state.YTState) {
	for {
		s.Mu.RLock()
		videoID := s.VideoID
		s.Mu.RUnlock()

		if videoID == "" {
			time.Sleep(5 * time.Second)
			continue
		}

		initMsgs, continuation := getLiveChatInit(videoID)
		if continuation == "" {
			time.Sleep(10 * time.Second)
			continue
		}

		// Process initial messages
		if len(initMsgs) > 0 {
			s.AppendChat(initMsgs)
		}

		// Create a persistent HTTP client for chat polling
		client := &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        1,
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     90 * time.Second,
			},
		}

		for {
			s.Mu.RLock()
			currentVID := s.VideoID
			s.Mu.RUnlock()
			if currentVID != videoID {
				break
			}

			payload, _ := json.Marshal(map[string]any{
				"context": map[string]any{
					"client": map[string]any{
						"clientName":    "WEB",
						"clientVersion": "2.20240313.05.00",
					},
				},
				"continuation": continuation,
			})

			req, _ := http.NewRequest("POST",
				"https://www.youtube.com/youtubei/v1/live_chat/get_live_chat",
				bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", userAgent)

			resp, err := client.Do(req)
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}

			var data map[string]any
			if err := json.Unmarshal(body, &data); err != nil {
				time.Sleep(3 * time.Second)
				continue
			}

			msgs, nextCont, pollMs := parseChatResponse(data)

			if len(msgs) > 0 {
				// Add timestamps
				now := time.Now().Format("15:04:05")
				for i := range msgs {
					msgs[i].Time = now
				}
				s.AppendChat(msgs)
			}

			if nextCont == "" {
				break
			}
			continuation = nextCont

			delay := time.Duration(pollMs) * time.Millisecond
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}
}

// ── JSON navigation helpers ─────────────────────────────────────────────────

// dig navigates nested maps by key path, returning nil if any step fails.
func dig(m map[string]any, keys ...string) any {
	var current any = m
	for _, k := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = cm[k]
	}
	return current
}

// digItem extracts the chat item from an action, checking addChatItemAction
// and replayChatItemAction.
func digItem(action map[string]any) map[string]any {
	if addItem, ok := action["addChatItemAction"].(map[string]any); ok {
		if item, ok := addItem["item"].(map[string]any); ok {
			return item
		}
	}
	if replay, ok := action["replayChatItemAction"].(map[string]any); ok {
		if subs, ok := replay["actions"].([]any); ok {
			for _, s := range subs {
				sub, _ := s.(map[string]any)
				if addItem, ok := sub["addChatItemAction"].(map[string]any); ok {
					if item, ok := addItem["item"].(map[string]any); ok {
						return item
					}
				}
			}
		}
	}
	return nil
}

// getChatRenderer returns the liveChatTextMessageRenderer or liveChatPaidMessageRenderer.
func getChatRenderer(item map[string]any) map[string]any {
	if r, ok := item["liveChatTextMessageRenderer"].(map[string]any); ok {
		return r
	}
	if r, ok := item["liveChatPaidMessageRenderer"].(map[string]any); ok {
		return r
	}
	return nil
}
