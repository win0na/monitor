// Package state holds shared application state for the Stream Monitor.
//
// All monitoring goroutines write to these structs through exported
// fields (protected by Mu), and the HTTP server reads from them via
// Snapshot methods. A single RWMutex per state group provides safe
// concurrent access.
package state

import (
	"sync"
	"time"
)

// ── OBS state ───────────────────────────────────────────────────────────────

// OBSState holds OBS WebSocket connection state and streaming statistics.
type OBSState struct {
	Mu        sync.RWMutex
	Connected bool
	Stats     map[string]any
	Stream    map[string]any
	Kbps      *float64
}

// Snapshot returns a JSON-safe copy of the current OBS state.
func (s *OBSState) Snapshot() map[string]any {
	s.Mu.RLock()
	defer s.Mu.RUnlock()

	return map[string]any{
		"connected": s.Connected,
		"stats":     s.Stats,
		"stream":    s.Stream,
		"kbps":      s.Kbps,
	}
}

// NewOBSState returns an initialised OBSState.
func NewOBSState() *OBSState {
	return &OBSState{
		Stats:  map[string]any{},
		Stream: map[string]any{},
	}
}

// ── YouTube state ───────────────────────────────────────────────────────────

// MessagePart represents a segment of a chat message — either plain text or an emoji image.
type MessagePart struct {
	Text  string `json:"text,omitempty"`
	Emoji string `json:"emoji,omitempty"` // image URL for custom/standard emojis
	Alt   string `json:"alt,omitempty"`   // display fallback (shortcut like ":smile:" or Unicode char)
}

// ChatMessage represents a single live chat message.
type ChatMessage struct {
	Author  string        `json:"author"`
	Message string        `json:"message"`
	Parts   []MessagePart `json:"parts,omitempty"`
	Role    string        `json:"role"`
	Time    string        `json:"time"`
}

// YTState holds YouTube live stream state: viewer count, chat messages, errors.
type YTState struct {
	Mu        sync.RWMutex
	Connected bool
	Error     *string
	Viewers   *string
	Chat      []ChatMessage
	ChatTotal int
	VideoID   string
}

// Snapshot returns a JSON-safe copy of the current YouTube state.
func (s *YTState) Snapshot() map[string]any {
	s.Mu.RLock()
	defer s.Mu.RUnlock()

	return map[string]any{
		"connected":  s.Connected,
		"error":      s.Error,
		"viewers":    s.Viewers,
		"chat":       s.Chat,
		"chat_total": s.ChatTotal,
		"video_id":   s.VideoID,
	}
}

// AppendChat adds messages to the chat buffer, capping at 200.
func (s *YTState) AppendChat(msgs []ChatMessage) {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	s.Chat = append(s.Chat, msgs...)
	if len(s.Chat) > 200 {
		s.Chat = s.Chat[len(s.Chat)-200:]
	}
	s.ChatTotal += len(msgs)
}

// NewYTState returns an initialised YTState.
func NewYTState() *YTState {
	return &YTState{
		Chat: []ChatMessage{},
	}
}

// ── GPU state ───────────────────────────────────────────────────────────────

// GPUState holds GPU utilisation percentage read from HWiNFO or nvidia-smi.
type GPUState struct {
	Mu    sync.RWMutex
	Pct   *float64
	Label *string
}

// Snapshot returns a JSON-safe copy of the current GPU state.
func (s *GPUState) Snapshot() map[string]any {
	s.Mu.RLock()
	defer s.Mu.RUnlock()

	return map[string]any{
		"pct":   s.Pct,
		"label": s.Label,
	}
}

// ── Boot time ───────────────────────────────────────────────────────────────

// BootTime is set once at startup so the frontend can detect server restarts.
var BootTime = time.Now().Unix()
