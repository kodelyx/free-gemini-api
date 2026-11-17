# fix(api): make /v1/chat/completions work reliably from agent harnesses (OpenCode, Hermes, AI-SDK clients)

While driving the server from local agent harnesses (OpenCode, Hermes), I hit four separate defects. Each is fixed in its own commit, with reproduction evidence and verification below. This partially overlaps #3 (which fixes some of the same streaming issues but does not address #1 and #4 below).

## 1. `Invalid request` from AI-SDK clients (OpenCode, Vercel AI, etc.)

`OpenAIChatMessage.Content` was `*string`. AI-SDK clients send message content as an **array of typed parts**:

```json
"content": [{"type": "text", "text": "Hello"}]
```

Binding that into `*string` fails and the endpoint returns `400 {"error": "Invalid request"}` — making the OpenAI-compatible endpoint unusable from these clients, which is ironic given they are the primary consumers of it.

**Fix:** `Content` is now `json.RawMessage` with a `FlattenContent` normalizer accepting both plain strings and part arrays. Outbound messages serialize as plain strings, unchanged.

```
# before
OpenCode: Error: Bad Request: Invalid request
# after
content-parts request -> "content parts OK."
```

## 2. Streaming ran on a dead context — empty streams, no retry possible

`HandleOpenAIChatCompletions` created `ctx` with `defer cancel()` and used it inside the `c.SendStreamWriter` closure. But the closure runs **after the handler returns**, so the context was already canceled when the stream tried to acquire a worker:

```
Streaming error: failed to acquire worker for streaming: context canceled
```

Result: `200` responses with **empty bodies** (`LEN: 0`) — clients see an empty stream with no `finish_reason`, which looks like a dead provider.

## 3. `c.IP()` / `c.Host()` inside stream closures panicked and killed the server

The same root cause: the fiber context is invalid once the handler returns. Calling `c.IP()` inside the stream-writer closure (in the post-stream logging goroutine) panicked, and since FastHTTP stream readers run on their own goroutines, this **crashed the entire server process**, dropping every in-flight request.

```
panic: ... chat_handler.go:527 (c.IP() inside SendStreamWriter closure)
```

**Fix (commits 2–3):** both `HandleUnifiedChat` and `HandleOpenAIChatCompletions` now create a fresh 5-minute context **inside** the stream closure, and request-derived values (`c.IP()`, `c.Host()`) are captured before the closure starts. `HandleUnifiedChat` had the identical latent bug and is fixed in the same commit.

## 4. Session continuation: ~130 KB re-pasted every turn → poor recall, empty outputs, canned refusals

Agent harnesses replay the full message history on every request. The handler flattened that history — Hermes' system prompt plus ~100 KB of tool JSON schemas plus the whole conversation — into **one giant user prompt** and re-pasted it into gemini.google.com on *every turn* (a trivial "what's my codeword?" turn carried ~137 KB of prompt).

gemini.google.com degrades badly under such giant pasted prompts:

- **Poor mid-context recall** ("the model doesn't seem to remember much") — the actual conversation is a tiny fraction at the end of a wall of text.
- **Intermittent canned refusals** — "I'm having a hard time fulfilling your request…" / "I seem to be encountering an error…" — and **thinking-only responses with empty text**, which downstream clients see as a dead provider.
- Compounding this, all local clients shared one upstream conversation (see #4a below), so once a conversation went bad, *every* subsequent request appended to the poisoned thread and kept being refused.

### 4a. One shared upstream conversation for all clients → context bleed-through

`GeminiClient` keeps sticky per-model conversation state and the worker pool shares one client; with session resolution falling back to `c.IP()`, every request from every client continued the **same single gemini.google.com conversation** (reproduced: a codeword from session A answered in session B).

### Fix: per-session upstream conversations + transcript-delta sends

Each logical session now owns its own upstream conversation, and only the **new transcript rows** are sent each turn:

- **Session key**: `X-Agent-ID` / `X-Session-ID` / `X-Conversation-ID` headers, then the OpenAI `user` field, then `session_id` query, then `c.IP()`. Harnesses can opt in to a real per-chat key (e.g. Hermes' `session_affinity_header: X-Session-ID`).
- **System-prompt fingerprinting**: harnesses fire auxiliary one-off requests (session titling) with a completely different system prompt through the same client; each prompt shape gets its own conversation thread so aux traffic never resets or pollutes the main thread.
- **Prefix-delta turns**: the flattened transcript is deterministic and harnesses replay history byte-stably, so the handler stores the last committed message-transcript per session; when the new transcript extends it, only the suffix (plus the trailing turn cue) is sent, continuing the live upstream conversation. The tool-schema block is sent once per conversation (full replays) instead of every turn. Anything else — new session, divergent/rewound/compacted history, worker without state — falls back to a **full replay on a fresh conversation**, so correctness never depends on the delta path.
- **Worker affinity + pointer restore**: session→worker affinity is bound on every success, and the recorded conversation pointer is restored into a freshly recreated worker (cookie re-syncs reload the pool and wipe client state, while the upstream conversation still lives on Google's servers).
- **Self-healing**: empty or canned-refusal outputs are never committed — the turn is invalidated and (when nothing has been streamed to the client yet, i.e. buffered tool-call streams and non-stream requests) retried invisibly with a full replay on a fresh conversation.

```
turn1 (full replay): 137,038 chars -> "Noted."
turn2 (recall):         76 chars delta -> correct answer
tool-result turn:      ~130 chars delta (was ~138,000 full replay)
fresh conversation:     "none"  <- no bleed-through
```

## Verification

`go build ./...` and `go vet ./...` clean. Live end-to-end against a running server with the cookie bridge + subscription account:

- OpenCode (`opencode run --model <provider>/gemini-4-argon`) → correct reply
- Hermes (`hermes -z ... -m argon-high`) → correct reply
- Content-parts binding → OK
- SSE streaming → proper chunks, `finish_reason: stop`, `[DONE]`
- Multi-turn delta continuation incl. tool-call/tool-result turns → OK
- Cross-session bleed-through → gone (fresh conversations answer "none")
- Blocked/refused generations → invisible retry on a fresh conversation

## Notes for the maintainer

- Commit 2 covers the same ground as parts of #3, but without the `[STREAM DEBUG]` logging and with both handlers fixed. Commit 1 (the OpenCode 400) and commit 4 (session continuation) are not covered by #3.
- No public API changes; the only wire-visible differences are that previously-failing AI-SDK requests now succeed, streams no longer come back empty, and upstream prompts shrink from ~137 KB/turn to a few hundred bytes after the first turn of a session.
