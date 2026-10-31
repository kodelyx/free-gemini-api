package api

import "strings"

type streamParseMode int

const (
	streamModeText streamParseMode = iota
	streamModeThought
	streamModeToolCall
)

type streamMarker struct {
	text string
	mode streamParseMode
}

type incrementalStreamParser struct {
	pending     string
	mode        streamParseMode
	thoughtEnd  string
	onText      func(string)
	onThought   func(string)
}

func newIncrementalStreamParser(
	onText func(string),
	onThought func(string),
) *incrementalStreamParser {
	return &incrementalStreamParser{
		onText:    onText,
		onThought: onThought,
	}
}

func (p *incrementalStreamParser) Write(chunk string) {
	p.pending += chunk
	p.drain(false)
}

func (p *incrementalStreamParser) emit(value string) {
	if value == "" {
		return
	}

	switch p.mode {
	case streamModeText:
		if p.onText != nil {
			p.onText(value)
		}
	case streamModeThought:
		if p.onThought != nil {
			p.onThought(value)
		}
	case streamModeToolCall:
		// Tool-call payloads must never be emitted as assistant text.
	}
}

func (p *incrementalStreamParser) markers() []streamMarker {
	switch p.mode {
	case streamModeText:
		return []streamMarker{
			{text: "<tool_call>", mode: streamModeToolCall},
			{text: "<thought>", mode: streamModeThought},
			{text: "<thinking>", mode: streamModeThought},
		}
	case streamModeThought:
		return []streamMarker{
			{text: p.thoughtEnd, mode: streamModeText},
		}
	case streamModeToolCall:
		return []streamMarker{
			{text: "</tool_call>", mode: streamModeText},
		}
	default:
		return nil
	}
}

func (p *incrementalStreamParser) drain(final bool) {
	for len(p.pending) > 0 {
		markers := p.markers()

		foundAt := -1
		var found streamMarker

		for _, marker := range markers {
			if marker.text == "" {
				continue
			}

			index := strings.Index(p.pending, marker.text)
			if index >= 0 && (foundAt < 0 || index < foundAt) {
				foundAt = index
				found = marker
			}
		}

		if foundAt >= 0 {
			p.emit(p.pending[:foundAt])
			p.pending = p.pending[foundAt+len(found.text):]

			if p.mode == streamModeText && found.mode == streamModeThought {
				if found.text == "<thought>" {
					p.thoughtEnd = "</thought>"
				} else {
					p.thoughtEnd = "</thinking>"
				}
			}

			p.mode = found.mode
			continue
		}

		if final {
			p.emit(p.pending)
			p.pending = ""
			return
		}

		// Retain a suffix that could be the beginning of a marker
		// split across two upstream chunks.
		keep := 0
		for _, marker := range markers {
			max := len(marker.text) - 1
			if max > len(p.pending) {
				max = len(p.pending)
			}

			for n := max; n > keep; n-- {
				if strings.HasSuffix(p.pending, marker.text[:n]) {
					keep = n
					break
				}
			}
		}

		if keep > 0 {
			p.emit(p.pending[:len(p.pending)-keep])
			p.pending = p.pending[len(p.pending)-keep:]
		} else {
			p.emit(p.pending)
			p.pending = ""
		}
		return
	}
}

func (p *incrementalStreamParser) Finish() {
	p.drain(true)
}
