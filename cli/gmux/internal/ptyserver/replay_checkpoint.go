package ptyserver

import "bytes"

var (
	replayBSU = []byte("\x1b[?2026h")
	replayESU = []byte("\x1b[?2026l")
)

const (
	// Large enough for multi-megabyte PNGs after Kitty/base64 framing, while
	// still bounding per-session runner memory. Oversized frames fall back to
	// the text snapshot rather than retaining an unsafe partial payload.
	replayCheckpointLimit = 16 << 20
	replaySuffixLimit     = 2 << 20
	replayCSILimit        = 1024
)

type streamParseState uint8

const (
	streamGround streamParseState = iota
	streamEscape
	streamCSI
	streamOSC
	streamDCS
	streamAPC
	streamPM
	streamSOS
	streamStringEscape
)

// terminalStreamParser tracks escape/control-string boundaries across PTY
// writes. Graphics payloads are opaque strings, so attach must wait until the
// parser returns to ground instead of starting a client inside Kitty/Sixel/IIP.
type terminalStreamParser struct {
	state        streamParseState
	stringParent streamParseState
	csi          []byte
	invalid      bool
}

func (p *terminalStreamParser) ground() bool { return p.state == streamGround }

func (p *terminalStreamParser) takeInvalid() bool {
	invalid := p.invalid
	p.invalid = false
	return invalid
}

// feed returns a completed 7-bit CSI body (for example "?2026h" or "2J").
func (p *terminalStreamParser) feed(b byte) []byte {
	switch p.state {
	case streamGround:
		if b == 0x1b {
			p.state = streamEscape
		}
	case streamEscape:
		switch b {
		case '[':
			p.state = streamCSI
			p.csi = p.csi[:0]
		case ']':
			p.state = streamOSC
		case 'P':
			p.state = streamDCS
		case '_':
			p.state = streamAPC
		case '^':
			p.state = streamPM
		case 'X':
			p.state = streamSOS
		case 0x1b:
			// A new ESC restarts the escape sequence.
		default:
			if b >= 0x30 && b <= 0x7e {
				p.state = streamGround
			}
		}
	case streamCSI:
		if b == 0x18 || b == 0x1a {
			p.state = streamGround
			p.csi = p.csi[:0]
			return nil
		}
		if b == 0x1b {
			p.state = streamEscape
			p.csi = p.csi[:0]
			return nil
		}
		if len(p.csi) == replayCSILimit {
			p.csi = p.csi[:0]
			p.state = streamGround
			p.invalid = true
			return nil
		}
		p.csi = append(p.csi, b)
		if b >= 0x40 && b <= 0x7e {
			token := bytes.Clone(p.csi)
			p.csi = p.csi[:0]
			p.state = streamGround
			return token
		}
	case streamOSC, streamDCS, streamAPC, streamPM, streamSOS:
		if b == 0x18 || b == 0x1a || (p.state == streamOSC && b == 0x07) {
			p.state = streamGround
			return nil
		}
		if b == 0x1b {
			p.stringParent = p.state
			p.state = streamStringEscape
		}
	case streamStringEscape:
		if b == '\\' || b == 0x18 || b == 0x1a {
			p.state = streamGround
		} else if b != 0x1b {
			p.state = p.stringParent
		}
	}
	return nil
}

// rawReplay retains the latest complete synchronized full redraw plus all
// subsequent bytes. It is guarded by Server.mu.
type rawReplay struct {
	parser terminalStreamParser

	checkpoint []byte
	suffix     []byte
	valid      bool

	candidate           []byte
	candidateOpen       bool
	candidateTooLarge   bool
	candidateErase      bool
	candidateHome       bool
	candidateScrollback bool
}

func (r *rawReplay) safeBoundary() bool {
	return !r.candidateOpen && r.parser.ground()
}

func (r *rawReplay) invalidate() {
	r.checkpoint = nil
	r.suffix = nil
	r.valid = false
}

func (r *rawReplay) abandonUnsafe() {
	r.invalidate()
	r.parser = terminalStreamParser{}
	r.candidate = nil
	r.candidateOpen = false
	r.candidateTooLarge = false
	r.candidateErase = false
	r.candidateHome = false
	r.candidateScrollback = false
}

func (r *rawReplay) appendSuffix(data []byte) {
	if !r.valid || len(data) == 0 {
		return
	}
	if len(r.suffix)+len(data) > replaySuffixLimit {
		r.invalidate()
		return
	}
	r.suffix = append(r.suffix, data...)
}

func (r *rawReplay) startCandidate() {
	r.candidateOpen = true
	r.candidateTooLarge = false
	r.candidateErase = false
	r.candidateHome = false
	r.candidateScrollback = false
	r.candidate = append(r.candidate[:0], replayBSU...)
}

func (r *rawReplay) appendCandidateByte(b byte) {
	if r.candidateTooLarge {
		return
	}
	if len(r.candidate) == replayCheckpointLimit {
		r.candidate = nil
		r.candidateTooLarge = true
		return
	}
	r.candidate = append(r.candidate, b)
}

func (r *rawReplay) finishCandidate() {
	if !r.candidateTooLarge && r.candidateErase && r.candidateHome && r.candidateScrollback {
		r.checkpoint = r.candidate
		r.suffix = nil
		r.valid = true
	} else if r.candidateTooLarge {
		// We cannot append a truncated synchronized frame to an older replay.
		r.invalidate()
	} else {
		r.appendSuffix(r.candidate)
	}
	r.candidate = nil
	r.candidateOpen = false
	r.candidateTooLarge = false
	r.candidateErase = false
	r.candidateHome = false
	r.candidateScrollback = false
}

func (r *rawReplay) write(data []byte) {
	for _, b := range data {
		if r.candidateOpen {
			r.appendCandidateByte(b)
		} else {
			r.appendSuffix([]byte{b})
		}

		token := r.parser.feed(b)
		if r.parser.takeInvalid() {
			r.invalidate()
		}
		if token == nil {
			continue
		}

		switch string(token) {
		case "?2026h":
			if r.candidateOpen {
				// A nested begin means the prior candidate cannot be replayed as
				// a complete delta. Drop the old replay and restart at this BSU.
				r.invalidate()
			}
			if r.valid && len(r.suffix) >= len(replayBSU) {
				r.suffix = r.suffix[:len(r.suffix)-len(replayBSU)]
			}
			r.startCandidate()
		case "2J":
			if r.candidateOpen {
				r.candidateErase = true
			}
		case "3J":
			if r.candidateOpen {
				r.candidateScrollback = true
			}
		case "H", "1;1H":
			if r.candidateOpen {
				r.candidateHome = true
			}
		case "?2026l":
			if r.candidateOpen {
				r.finishCandidate()
			}
		}
	}
}

func (r *rawReplay) parts() (checkpoint, suffix []byte) {
	if !r.valid || !r.safeBoundary() {
		return nil, nil
	}
	// Callers hold Server.mu until these immutable slices are written.
	return r.checkpoint, r.suffix
}

func (r *rawReplay) bytes() []byte {
	checkpoint, suffix := r.parts()
	if len(checkpoint) == 0 {
		return nil
	}
	out := make([]byte, 0, len(checkpoint)+len(suffix))
	out = append(out, checkpoint...)
	return append(out, suffix...)
}
