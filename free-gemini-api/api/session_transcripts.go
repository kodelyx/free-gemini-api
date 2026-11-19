package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// Per-session transcript bookkeeping for the OpenAI-compatible endpoint.
//
// Agent harnesses replay the full message history on every request, and the
// handler flattens that history into one big prompt. Re-pasting a huge
// flattened transcript (system prompt + tool schemas + whole conversation)
// into gemini.google.com on every turn degrades quality and intermittently
// triggers canned refusals or empty responses.
//
// Instead, each session keeps an upstream conversation alive and only the
// delta (the suffix of the flattened transcript since the last committed
// turn) is sent. A turn is committed only when the model produced usable
// output; on failure the upstream conversation is invalidated so the next
// request resets it and replays the full transcript. Because the transcript
// is flattened deterministically and clients replay history byte-stably, a
// simple prefix match decides whether the delta is safe. Anything else (new
// session, rewound/compacted history, worker conversation mismatch) falls
// back to a full replay on a fresh upstream conversation.

type sessionTurnEntry struct {
	lastFlat     string // flattened prompt sent upstream on the last committed turn
	lastConvID   string // upstream conversation the turn was committed into
	lastRespID   string // upstream response pointer
	lastChoiceID string // upstream choice pointer
	lastWorkerID string // pool worker (account) that holds the client-side state
	convValid    bool   // false after a failed/blocked turn: conversation must be reset
}

var (
	sessionTurnMu    sync.Mutex
	sessionTurnState = map[string]*sessionTurnEntry{}
)

// maxTrackedSessions bounds memory; eviction is a simple delete-one policy —
// evicted sessions simply fall back to a full replay on their next turn.
const maxTrackedSessions = 256

// planSessionTurn decides what to send upstream:
//   - the full flattened transcript plus a conversation reset (new session,
//     divergent history, or dead conversation), or
//   - just the delta since the last committed turn, continuing the live
//     upstream conversation.
//
// curConvID is the live conversation id on the worker that will serve the
// request. A delta only requires that the worker still holds some live
// conversation for this session: the upstream conversation may legitimately
// have advanced since the last main turn (id rotation per response, or
// harness side-requests such as session titling share the session key), and
// sending the latest state id continues that same thread. An empty id means
// the worker has no conversation for this session, so a full replay is due.
func planSessionTurn(sessionKey, curConvID, flat string) (sendPrompt string, needReset bool) {
	sessionTurnMu.Lock()
	defer sessionTurnMu.Unlock()

	e := sessionTurnState[sessionKey]
	if e == nil || !e.convValid || e.lastFlat == "" {
		return flat, true
	}
	if e.lastConvID == "" || curConvID == "" {
		return flat, true
	}
	if flat == e.lastFlat {
		// Identical transcript (e.g. client retry after a client-side error):
		// replay in full on a reset conversation so the turn is not dropped.
		return flat, true
	}
	if strings.HasPrefix(flat, e.lastFlat) {
		return flat[len(e.lastFlat):], false
	}
	return flat, true
}

// systemScopeKey derives a short stable fingerprint from a request's system
// prompt. Harnesses send auxiliary one-off requests (session titling,
// summarizers) with a completely different system prompt through the same
// client; fingerprinting the system prompt gives each prompt shape its own
// upstream conversation thread so aux traffic never resets or pollutes the
// main chat thread.
func systemScopeKey(systemContent string) string {
	sum := sha256.Sum256([]byte(systemContent))
	return hex.EncodeToString(sum[:8])
}

// contentScopeKey returns a short stable fingerprint of an arbitrary string.
// Used to derive a per-conversation scope from the first user message when a
// client sends no explicit affinity identifier.
func contentScopeKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// commitSessionTurn records a successful turn so subsequent requests can be
// sent as deltas continuing the given upstream conversation. The full
// conversation pointer (conv/resp/choice ids) and the owning worker are kept
// so the pointer can be restored after a pool reload recreates workers.
func commitSessionTurn(sessionKey, flat, convID, respID, choiceID, workerID string) {
	sessionTurnMu.Lock()
	defer sessionTurnMu.Unlock()
	if len(sessionTurnState) >= maxTrackedSessions {
		for k := range sessionTurnState {
			delete(sessionTurnState, k)
			break
		}
	}
	sessionTurnState[sessionKey] = &sessionTurnEntry{
		lastFlat:     flat,
		lastConvID:   convID,
		lastRespID:   respID,
		lastChoiceID: choiceID,
		lastWorkerID: workerID,
		convValid:    true,
	}
}

// peekSessionTurnConv returns the recorded upstream conversation pointer for
// a session, for callers that want to restore it into a freshly recreated
// worker client before planning the turn.
func peekSessionTurnConv(sessionKey string) (convID, respID, choiceID, workerID string, convValid bool) {
	sessionTurnMu.Lock()
	defer sessionTurnMu.Unlock()
	if e := sessionTurnState[sessionKey]; e != nil {
		return e.lastConvID, e.lastRespID, e.lastChoiceID, e.lastWorkerID, e.convValid
	}
	return "", "", "", "", false
}

// invalidateSessionTurn marks the session's upstream conversation unusable
// (request failed or the model produced no usable output), forcing the next
// request to reset and replay the full transcript.
func invalidateSessionTurn(sessionKey string) {
	sessionTurnMu.Lock()
	defer sessionTurnMu.Unlock()
	if e := sessionTurnState[sessionKey]; e != nil {
		e.convValid = false
	}
}

// isUnusableModelOutput reports whether the model output is empty or one of
// gemini.google.com's canned failure strings. Such turns are not committed,
// so the next request starts a fresh upstream conversation with a full replay.
func isUnusableModelOutput(text string) bool {
	if strings.TrimSpace(text) == "" {
		return true
	}
	canned := []string{
		"hard time fulfilling your request",
		"I seem to be encountering an error",
	}
	for _, phrase := range canned {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
