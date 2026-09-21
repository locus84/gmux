package ptyserver

import (
	"container/list"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	imageCacheLimit = 32 << 20
	// imagePNGByteLimit is the compressed PNG size reported as metadata.bytes.
	// Base64 framing is bounded separately at the exact ceiling needed to carry
	// that many PNG bytes.
	imagePNGByteLimit     = 8 << 20
	imageBase64Limit      = ((imagePNGByteLimit + 2) / 3) * 4
	imageTransferRawLimit = imageBase64Limit + (256 << 10)
	imageDimensionLimit   = 8192
	imagePixelLimit       = 16_000_000
	imageExpiredLimit     = 1024
	imageMetadataLimit    = 1000
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

type imageCacheEntry struct {
	hash string
	data []byte
}

type imageCache struct {
	mu      sync.Mutex
	limit   int
	bytes   int
	entries map[string]*list.Element
	lru     list.List
	expired map[string]*list.Element
	expLRU  list.List
}

type expiredHash struct{ hash string }

func newImageCache(limit int) *imageCache {
	return &imageCache{limit: limit, entries: make(map[string]*list.Element), expired: make(map[string]*list.Element)}
}

func (c *imageCache) put(data []byte) string {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem := c.entries[hash]; elem != nil {
		c.lru.MoveToFront(elem)
		return hash
	}
	copyData := append([]byte(nil), data...)
	elem := c.lru.PushFront(&imageCacheEntry{hash: hash, data: copyData})
	c.entries[hash] = elem
	c.bytes += len(copyData)
	if old := c.expired[hash]; old != nil {
		c.expLRU.Remove(old)
		delete(c.expired, hash)
	}
	for c.bytes > c.limit && c.lru.Len() > 0 {
		victim := c.lru.Back()
		entry := victim.Value.(*imageCacheEntry)
		c.lru.Remove(victim)
		delete(c.entries, entry.hash)
		c.bytes -= len(entry.data)
		c.rememberExpired(entry.hash)
	}
	return hash
}

func (c *imageCache) rememberExpired(hash string) {
	if elem := c.expired[hash]; elem != nil {
		c.expLRU.MoveToFront(elem)
		return
	}
	c.expired[hash] = c.expLRU.PushFront(expiredHash{hash})
	for c.expLRU.Len() > imageExpiredLimit {
		old := c.expLRU.Back()
		delete(c.expired, old.Value.(expiredHash).hash)
		c.expLRU.Remove(old)
	}
}

func (c *imageCache) get(hash string) (data []byte, found, expired bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem := c.entries[hash]; elem != nil {
		c.lru.MoveToFront(elem)
		return append([]byte(nil), elem.Value.(*imageCacheEntry).data...), true, false
	}
	_, expired = c.expired[hash]
	return nil, false, expired
}

type imageReference struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Hash    string `json:"hash,omitempty"`
	ID      uint32 `json:"id,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	Mime    string `json:"mime,omitempty"`
	All     bool   `json:"all,omitempty"`
}

type kittyTransfer struct {
	raw             []byte
	encoded         []byte
	id              uint32
	cols            int
	rows            int
	cleanupPrevious bool
}

type imageMetadata struct {
	id            uint32
	hash          string
	bytes         int
	width, height int
}

type imageMetadataMap struct {
	limit   int
	entries map[uint32]*list.Element
	lru     list.List
}

func newImageMetadataMap(limit int) *imageMetadataMap {
	return &imageMetadataMap{limit: limit, entries: make(map[uint32]*list.Element)}
}

func (m *imageMetadataMap) put(metadata imageMetadata) {
	if elem := m.entries[metadata.id]; elem != nil {
		elem.Value = metadata
		m.lru.MoveToFront(elem)
	} else {
		m.entries[metadata.id] = m.lru.PushFront(metadata)
	}
	for m.lru.Len() > m.limit {
		old := m.lru.Back()
		delete(m.entries, old.Value.(imageMetadata).id)
		m.lru.Remove(old)
	}
}

func (m *imageMetadataMap) get(id uint32) (imageMetadata, bool) {
	elem := m.entries[id]
	if elem == nil {
		return imageMetadata{}, false
	}
	m.lru.MoveToFront(elem)
	return elem.Value.(imageMetadata), true
}

func (m *imageMetadataMap) delete(id uint32) bool {
	if elem := m.entries[id]; elem != nil {
		m.lru.Remove(elem)
		delete(m.entries, id)
		return true
	}
	return false
}

func (m *imageMetadataMap) clear() {
	m.entries = make(map[uint32]*list.Element)
	m.lru.Init()
}

type imageTransformer struct {
	cache    *imageCache
	metadata *imageMetadataMap
	// scan tracks all non-Kitty terminal strings. A ground-state check is
	// load-bearing: ESC_G text nested inside OSC/DCS/APC payloads is data, not a
	// second control string, and must never be rewritten.
	scan terminalStreamParser
	// pending holds a ground-state ESC prefix or one Kitty APC while it is
	// recognized. transfer spans supported m=1/m=0 commands.
	pending            []byte
	inKitty            bool
	passthroughKitty   bool
	passthroughESC     bool
	passthroughCleanup uint32
	transfer           *kittyTransfer
}

func newImageTransformer(cache *imageCache) *imageTransformer {
	return &imageTransformer{cache: cache, metadata: newImageMetadataMap(imageMetadataLimit)}
}

func (t *imageTransformer) safeBoundary() bool {
	return len(t.pending) == 0 && !t.inKitty && !t.passthroughKitty && t.transfer == nil && t.scan.ground()
}

// flush returns an incomplete candidate verbatim. It is used only at PTY EOF.
func (t *imageTransformer) flush() []byte {
	transfer := t.transfer
	out := append([]byte(nil), t.transferRaw()...)
	out = append(out, t.pending...)
	if transfer != nil && transfer.cleanupPrevious {
		out = append(out, encodeImageDelete(transfer.id)...)
	} else if t.passthroughCleanup != 0 {
		out = append(out, encodeImageDelete(t.passthroughCleanup)...)
	}
	t.pending = nil
	t.inKitty = false
	t.passthroughKitty = false
	t.passthroughESC = false
	t.passthroughCleanup = 0
	t.transfer = nil
	return out
}

func (t *imageTransformer) transferRaw() []byte {
	if t.transfer == nil {
		return nil
	}
	return t.transfer.raw
}

func (t *imageTransformer) feed(data []byte) []byte {
	var out []byte
	writeRaw := func(raw []byte) {
		var cleanupID uint32
		if t.transfer != nil {
			transfer := t.transfer
			out = append(out, transfer.raw...)
			t.transfer = nil
			if transfer.cleanupPrevious {
				cleanupID = transfer.id
			}
		}
		out = append(out, raw...)
		if cleanupID != 0 {
			out = append(out, encodeImageDelete(cleanupID)...)
		}
		for _, b := range raw {
			t.scan.feed(b)
			t.scan.takeInvalid()
		}
	}
	for _, b := range data {
		if t.passthroughKitty {
			out = append(out, b)
			if b == 0x18 || b == 0x1a || (t.passthroughESC && b == '\\') {
				t.passthroughKitty = false
				t.passthroughESC = false
				if t.passthroughCleanup != 0 {
					out = append(out, encodeImageDelete(t.passthroughCleanup)...)
					t.passthroughCleanup = 0
				}
				continue
			}
			t.passthroughESC = b == 0x1b
			continue
		}
		if t.inKitty {
			t.pending = append(t.pending, b)
			if b == 0x18 || b == 0x1a {
				writeRaw(t.pending)
				t.pending = nil
				t.inKitty = false
				continue
			}
			if len(t.pending) >= 2 && t.pending[len(t.pending)-2] == 0x1b && b == '\\' {
				cmd := t.pending
				t.pending = nil
				t.inKitty = false
				out = append(out, t.completeKitty(cmd)...)
				continue
			}
			if len(t.pending) > imageTransferRawLimit {
				if t.transfer != nil && t.transfer.cleanupPrevious {
					t.passthroughCleanup = t.transfer.id
				} else if id, reupload := pendingKittyReuploadID(t.pending); reupload && t.metadata.delete(id) {
					t.passthroughCleanup = id
				}
				out = append(out, t.transferRaw()...)
				out = append(out, t.pending...)
				t.pending = nil
				t.transfer = nil
				t.inKitty = false
				t.passthroughKitty = true
				t.passthroughESC = b == 0x1b
			}
			continue
		}

		if len(t.pending) == 0 {
			if t.scan.ground() && b == 0x1b {
				t.pending = append(t.pending, b)
				continue
			}
			writeRaw([]byte{b})
			continue
		}

		// Only ground-state ESC _ G starts Kitty graphics. Hold at most that
		// prefix; on a mismatch feed every byte through the ordinary parser.
		want := []byte{0x1b, '_', 'G'}
		if len(t.pending) < len(want) && b == want[len(t.pending)] {
			t.pending = append(t.pending, b)
			if len(t.pending) == len(want) {
				t.inKitty = true
			}
			continue
		}
		raw := append(append([]byte(nil), t.pending...), b)
		t.pending = nil
		writeRaw(raw)
	}
	return out
}

func parseKittyCommand(raw []byte) (map[string]string, []byte, bool) {
	if len(raw) < 5 || string(raw[:3]) != "\x1b_G" || string(raw[len(raw)-2:]) != "\x1b\\" {
		return nil, nil, false
	}
	body := raw[3 : len(raw)-2]
	semi := -1
	for i, b := range body {
		if b == ';' {
			semi = i
			break
		}
	}
	header, payload := body, []byte(nil)
	if semi >= 0 {
		header, payload = body[:semi], body[semi+1:]
	}
	params, ok := parseKittyParams(header)
	return params, payload, ok
}

func parseKittyParams(header []byte) (map[string]string, bool) {
	params := make(map[string]string)
	if len(header) == 0 {
		return params, true
	}
	for _, field := range strings.Split(string(header), ",") {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" || value == "" {
			return nil, false
		}
		if _, duplicate := params[key]; duplicate {
			return nil, false
		}
		params[key] = value
	}
	return params, true
}

func pendingKittyReuploadID(raw []byte) (uint32, bool) {
	if len(raw) < 4 || string(raw[:3]) != "\x1b_G" {
		return 0, false
	}
	for i, b := range raw[3:] {
		if b != ';' {
			continue
		}
		params, ok := parseKittyParams(raw[3 : i+3])
		if !ok {
			return 0, false
		}
		return kittyReuploadID(params)
	}
	return 0, false
}

func onlyKeys(params map[string]string, allowed ...string) bool {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range params {
		if !set[key] {
			return false
		}
	}
	return true
}

func positiveInt(s string, max int) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0 && n <= max
}

func positiveUint32(s string) (uint32, bool) {
	n, err := strconv.ParseUint(s, 10, 32)
	return uint32(n), err == nil && n > 0
}

func nonnegativeInt(s string, max int) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0 && n <= max
}

func (t *imageTransformer) completeKitty(raw []byte) []byte {
	params, payload, ok := parseKittyCommand(raw)
	if !ok {
		return t.fallback(raw)
	}
	if t.transfer != nil {
		if !onlyKeys(params, "m") || (params["m"] != "0" && params["m"] != "1") || len(payload) == 0 {
			return t.fallback(raw)
		}
		if len(t.transfer.raw)+len(raw) > imageTransferRawLimit || len(t.transfer.encoded)+len(payload) > imageBase64Limit {
			return t.fallback(raw)
		}
		t.transfer.raw = append(t.transfer.raw, raw...)
		t.transfer.encoded = append(t.transfer.encoded, payload...)
		if params["m"] == "1" {
			return nil
		}
		transfer := t.transfer
		t.transfer = nil
		return t.finishTransfer(transfer)
	}

	if ref, supported := t.kittyPlacementReference(params, payload); supported {
		return encodeImageReference(ref)
	}
	// Apply data-deletion semantics even if an extension makes the command too
	// broad for OSC translation. The native addon will still execute the raw
	// command, so retaining that ID would make a later placement stale.
	t.applyKittyDeleteToMetadata(params)
	if ref, supported := kittyDeleteReference(params, payload); supported {
		// The native addon must still see deletes for unsupported/raw Kitty
		// images that can coexist with refs. The OSC is an additional overlay
		// cleanup signal, not a replacement for the native command.
		return append(append([]byte(nil), raw...), encodeImageReference(ref)...)
	}
	// A new transmission for an existing ID supersedes its cached data. Drop
	// metadata before validating the mode so an unsupported reupload can never
	// leave an old hash behind for a later a=p command.
	reuploadID, reupload := kittyReuploadID(params)
	cleanupPrevious := false
	if reupload {
		cleanupPrevious = t.metadata.delete(reuploadID)
	}
	multipart := params["m"]
	if multipart == "" {
		multipart = "0" // omitted on Pi's single-chunk direct transmissions
	}
	if !onlyKeys(params, "a", "f", "q", "C", "c", "r", "i", "m", "t") ||
		params["a"] != "T" || params["f"] != "100" || params["q"] != "2" || params["C"] != "1" ||
		(params["t"] != "" && params["t"] != "d") || (multipart != "0" && multipart != "1") || len(payload) == 0 {
		return rawWithImageDelete(raw, reuploadID, cleanupPrevious)
	}
	cols, colsOK := positiveInt(params["c"], 1000)
	rows, rowsOK := positiveInt(params["r"], 1000)
	id, idOK := positiveUint32(params["i"])
	if !colsOK || !rowsOK || !idOK {
		return rawWithImageDelete(raw, reuploadID, cleanupPrevious)
	}
	transfer := &kittyTransfer{raw: append([]byte(nil), raw...), encoded: append([]byte(nil), payload...), id: id, cols: cols, rows: rows, cleanupPrevious: cleanupPrevious}
	if len(transfer.encoded) > imageBase64Limit {
		return rawWithImageDelete(raw, reuploadID, cleanupPrevious)
	}
	if multipart == "1" {
		t.transfer = transfer
		return nil
	}
	return t.finishTransfer(transfer)
}

func kittyReuploadID(params map[string]string) (uint32, bool) {
	switch params["a"] {
	case "T", "t":
		id, ok := positiveUint32(params["i"])
		return id, ok
	default:
		return 0, false
	}
}

func (t *imageTransformer) kittyPlacementReference(params map[string]string, payload []byte) (imageReference, bool) {
	if len(payload) != 0 || params["a"] != "p" || params["q"] != "2" || params["C"] != "1" ||
		!onlyKeys(params, "a", "q", "C", "c", "r", "i", "y", "h") {
		return imageReference{}, false
	}
	id, idOK := positiveUint32(params["i"])
	cols, colsOK := positiveInt(params["c"], 1000)
	rows, rowsOK := positiveInt(params["r"], 1000)
	if !idOK || !colsOK || !rowsOK {
		return imageReference{}, false
	}
	metadata, ok := t.metadata.get(id)
	if !ok {
		return imageReference{}, false
	}
	yRaw, hasY := params["y"]
	hRaw, hasH := params["h"]
	if hasY != hasH {
		return imageReference{}, false
	}
	if hasY {
		y, yOK := nonnegativeInt(yRaw, metadata.height)
		h, hOK := positiveInt(hRaw, metadata.height)
		if !yOK || !hOK || y >= metadata.height || h > metadata.height-y {
			return imageReference{}, false
		}
	}
	// Cropping affects terminal presentation only. The compact placeholder uses
	// the cropped c/r cell geometry, while activation opens the full original
	// PNG identified by hash; the runner never rehydrates pixels into the PTY.
	return imageReference{Version: 1, Action: "put", Hash: metadata.hash, ID: id, Cols: cols, Rows: rows, Bytes: metadata.bytes, Mime: "image/png"}, true
}

func (t *imageTransformer) applyKittyDeleteToMetadata(params map[string]string) {
	if params["a"] != "d" {
		return
	}
	switch params["d"] {
	case "I":
		if id, ok := positiveUint32(params["i"]); ok {
			t.metadata.delete(id)
		}
	case "A":
		t.metadata.clear()
	}
}

func rawWithImageDelete(raw []byte, id uint32, cleanup bool) []byte {
	if !cleanup {
		return raw
	}
	out := append([]byte(nil), raw...)
	return append(out, encodeImageDelete(id)...)
}

func encodeImageDelete(id uint32) []byte {
	return encodeImageReference(imageReference{Version: 1, Action: "delete", ID: id})
}

func (t *imageTransformer) fallback(raw []byte) []byte {
	if t.transfer == nil {
		return raw
	}
	transfer := t.transfer
	out := append([]byte(nil), transfer.raw...)
	out = append(out, raw...)
	t.transfer = nil
	if transfer.cleanupPrevious {
		out = append(out, encodeImageDelete(transfer.id)...)
	}
	return out
}

func kittyDeleteReference(params map[string]string, payload []byte) (imageReference, bool) {
	if len(payload) != 0 || params["a"] != "d" || params["q"] != "2" || !onlyKeys(params, "a", "d", "i", "q") {
		return imageReference{}, false
	}
	switch params["d"] {
	case "I":
		id, ok := positiveUint32(params["i"])
		if !ok {
			return imageReference{}, false
		}
		return imageReference{Version: 1, Action: "delete", ID: id}, true
	case "A", "a":
		if params["i"] != "" {
			return imageReference{}, false
		}
		return imageReference{Version: 1, Action: "delete", All: true}, true
	default:
		return imageReference{}, false
	}
}

func (t *imageTransformer) finishTransfer(transfer *kittyTransfer) []byte {
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(transfer.encoded)))
	n, err := base64.StdEncoding.Decode(decoded, transfer.encoded)
	if err != nil || n > imagePNGByteLimit {
		return rawWithImageDelete(transfer.raw, transfer.id, transfer.cleanupPrevious)
	}
	decoded = decoded[:n]
	width, height, ok := pngDimensions(decoded)
	if !ok {
		return rawWithImageDelete(transfer.raw, transfer.id, transfer.cleanupPrevious)
	}
	hash := t.cache.put(decoded)
	t.metadata.put(imageMetadata{id: transfer.id, hash: hash, bytes: len(decoded), width: width, height: height})
	return encodeImageReference(imageReference{Version: 1, Action: "put", Hash: hash, ID: transfer.id, Cols: transfer.cols, Rows: transfer.rows, Bytes: len(decoded), Mime: "image/png"})
}

func pngDimensions(data []byte) (int, int, bool) {
	if len(data) < 33 || string(data[:8]) != string(pngSignature) || binary.BigEndian.Uint32(data[8:12]) != 13 || string(data[12:16]) != "IHDR" {
		return 0, 0, false
	}
	width := binary.BigEndian.Uint32(data[16:20])
	height := binary.BigEndian.Uint32(data[20:24])
	if width == 0 || height == 0 || width > imageDimensionLimit || height > imageDimensionLimit || uint64(width)*uint64(height) > imagePixelLimit {
		return 0, 0, false
	}
	return int(width), int(height), true
}

func validPNG(data []byte) bool {
	_, _, ok := pngDimensions(data)
	return ok
}

func encodeImageReference(ref imageReference) []byte {
	body, _ := json.Marshal(ref)
	out := []byte("\x1b]777;gmux-image;")
	out = append(out, body...)
	return append(out, '\x1b', '\\')
}

func validImageHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !validImageHash(hash) {
		writeRunnerImageError(w, http.StatusBadRequest, "invalid_hash", "image hash must be 64 lowercase hexadecimal characters")
		return
	}
	data, found, expired := s.images.get(hash)
	if !found {
		if expired {
			writeRunnerImageError(w, http.StatusGone, "image_expired", "image was evicted from the runner cache")
		} else {
			writeRunnerImageError(w, http.StatusNotFound, "image_not_found", "image is not present in the runner cache")
		}
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeRunnerImageError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
}
