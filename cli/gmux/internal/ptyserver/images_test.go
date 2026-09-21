package ptyserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testPNG(width, height uint32, extra int) []byte {
	b := make([]byte, 33+extra)
	copy(b, pngSignature)
	binary.BigEndian.PutUint32(b[8:12], 13)
	copy(b[12:16], "IHDR")
	binary.BigEndian.PutUint32(b[16:20], width)
	binary.BigEndian.PutUint32(b[20:24], height)
	b[24] = 8 // bit depth
	b[25] = 6 // RGBA
	return b
}

func kitty(header string, payload []byte) []byte {
	return []byte("\x1b_G" + header + ";" + string(payload) + "\x1b\\")
}

func kittyNoPayload(header string) []byte {
	return []byte("\x1b_G" + header + "\x1b\\")
}

func transformAll(t *testing.T, tr *imageTransformer, chunks ...[]byte) []byte {
	t.Helper()
	var out []byte
	for _, chunk := range chunks {
		out = append(out, tr.feed(chunk)...)
	}
	out = append(out, tr.flush()...)
	return out
}

func decodeRef(t *testing.T, output []byte) imageReference {
	t.Helper()
	prefix := []byte("\x1b]777;gmux-image;")
	if !bytes.HasPrefix(output, prefix) || !bytes.HasSuffix(output, []byte("\x1b\\")) {
		t.Fatalf("not an image reference: %q", output)
	}
	var ref imageReference
	if err := json.Unmarshal(output[len(prefix):len(output)-2], &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestImageTransformerPiDirectPNGAndEverySplit(t *testing.T) {
	png := testPNG(1440, 1440, 91)
	encoded := []byte(base64.StdEncoding.EncodeToString(png))
	// Headers are from a captured Pi transmission. Continuations intentionally
	// carry only m, as the installed Pi encoder does.
	cut := len(encoded) / 2
	raw := append(kitty("a=T,f=100,q=2,C=1,c=47,r=20,i=3880550720,m=1", encoded[:cut]),
		kitty("m=0", encoded[cut:])...)
	for split := 0; split <= len(raw); split++ {
		cache := newImageCache(imageCacheLimit)
		got := transformAll(t, newImageTransformer(cache), raw[:split], raw[split:])
		ref := decodeRef(t, got)
		if ref.Version != 1 || ref.Action != "put" || ref.ID != 3880550720 || ref.Cols != 47 || ref.Rows != 20 || ref.Bytes != len(png) || ref.Mime != "image/png" || !validImageHash(ref.Hash) {
			t.Fatalf("split %d: ref=%+v", split, ref)
		}
		stored, ok, expired := cache.get(ref.Hash)
		if !ok || expired || !bytes.Equal(stored, png) {
			t.Fatalf("split %d: cache mismatch (ok=%v expired=%v)", split, ok, expired)
		}
	}
}

func TestImageTransformerCapturedRealPiBytes(t *testing.T) {
	fixture := os.Getenv("GMUX_IMAGE_FIXTURE")
	if fixture == "" {
		t.Skip("set GMUX_IMAGE_FIXTURE to an optional locally captured Pi stream")
	}
	raw, err := os.ReadFile(fixture)
	if os.IsNotExist(err) {
		t.Skip("local captured Pi fixture is not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	cache := newImageCache(imageCacheLimit)
	tr := newImageTransformer(cache)
	var got []byte
	// One-byte feeding exercises arbitrary PTY fragmentation against the exact
	// 111-command stream emitted by the installed Pi terminal-image package.
	for _, b := range raw {
		got = append(got, tr.feed([]byte{b})...)
	}
	got = append(got, tr.flush()...)
	t.Logf("captured Pi image: %d raw bytes -> %d reference bytes (%.2f%% reduction)", len(raw), len(got), 100*(1-float64(len(got))/float64(len(raw))))
	ref := decodeRef(t, got)
	if ref.ID != 3880550720 || ref.Cols != 47 || ref.Rows != 20 || ref.Mime != "image/png" {
		t.Fatalf("captured ref=%+v", ref)
	}
	data, ok, expired := cache.get(ref.Hash)
	if !ok || expired || len(data) != ref.Bytes || !validPNG(data) {
		t.Fatalf("captured cache: bytes=%d ok=%v expired=%v", len(data), ok, expired)
	}
}

func TestImageTransformerAcceptsPiSingleChunkWithoutM(t *testing.T) {
	png := testPNG(2, 3, 0)
	raw := kitty("a=T,f=100,q=2,C=1,c=4,r=2,i=7", []byte(base64.StdEncoding.EncodeToString(png)))
	ref := decodeRef(t, transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), raw))
	if ref.ID != 7 || ref.Bytes != len(png) {
		t.Fatalf("ref=%+v", ref)
	}
}

func TestImageTransformerUnsupportedAndInvalidAreByteExact(t *testing.T) {
	png64 := base64.StdEncoding.EncodeToString(testPNG(1, 1, 0))
	cases := map[string][]byte{
		"file transport": kitty("a=T,t=f,f=100,q=2,C=1,c=1,r=1,i=1", []byte("L3RtcC94")),
		"cursor moves":   kitty("a=T,f=100,q=2,C=0,c=1,r=1,i=1", []byte(png64)),
		"missing cols":   kitty("a=T,f=100,q=2,C=1,r=1,i=1", []byte(png64)),
		"bad base64":     kitty("a=T,f=100,q=2,C=1,c=1,r=1,i=1", []byte("!!!!")),
		"bad png":        kitty("a=T,f=100,q=2,C=1,c=1,r=1,i=1", []byte(base64.StdEncoding.EncodeToString([]byte("not png")))),
		"malformed":      []byte("before\x1b_Ga=T,noequals;abc\x1b\\after"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			for split := 0; split <= len(raw); split++ {
				got := transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), raw[:split], raw[split:])
				if !bytes.Equal(got, raw) {
					t.Fatalf("split %d changed bytes\n got %q\nwant %q", split, got, raw)
				}
			}
		})
	}
}

func TestImageTransformerOverLimitAndBrokenMultipartPassThrough(t *testing.T) {
	over := kitty("a=T,f=100,q=2,C=1,c=1,r=1,i=1", bytes.Repeat([]byte{'A'}, imageBase64Limit+1))
	if got := transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), over); !bytes.Equal(got, over) {
		t.Fatal("over-limit command changed")
	}
	tr := newImageTransformer(newImageCache(imageCacheLimit))
	prior := kitty("a=T,f=100,q=2,C=1,c=1,r=1,i=1", []byte(base64.StdEncoding.EncodeToString(testPNG(1, 1, 0))))
	_ = decodeRef(t, tr.feed(prior))
	gotOver := transformAll(t, tr, over)
	if !bytes.HasPrefix(gotOver, over) {
		t.Fatal("over-limit reupload changed native bytes")
	}
	cleanup := decodeRef(t, gotOver[len(over):])
	if cleanup.Action != "delete" || cleanup.ID != 1 {
		t.Fatalf("over-limit cleanup=%+v", cleanup)
	}
	first := kitty("a=T,f=100,q=2,C=1,c=1,r=1,i=1,m=1", []byte("aVZC"))
	broken := append(append([]byte(nil), first...), []byte("ordinary output")...)
	if got := transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), broken[:len(first)+2], broken[len(first)+2:]); !bytes.Equal(got, broken) {
		t.Fatalf("broken multipart changed: %q", got)
	}
}

func TestImageTransformerDoesNotRecognizeNestedKittyText(t *testing.T) {
	looksLikeKitty := kitty("a=T,f=100,q=2,C=1,c=1,r=1,i=9", []byte(base64.StdEncoding.EncodeToString(testPNG(1, 1, 0))))
	cases := [][]byte{
		append(append([]byte("\x1b]777;payload="), looksLikeKitty...), []byte("\x1b\\tail")...),
		append(append([]byte("\x1bPprivate="), looksLikeKitty...), []byte("\x1b\\tail")...),
	}
	for _, raw := range cases {
		got := transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), raw)
		if !bytes.Equal(got, raw) {
			t.Fatalf("nested control string changed\n got %q\nwant %q", got, raw)
		}
	}
}

func TestImageTransformerPiCachedPlacementSurvivesPlacementDeleteAndCacheEviction(t *testing.T) {
	// Pi uploads once, clears only placements during redraw, then emits a=p for
	// the cached image. A one-byte cache forces the HTTP object into the typed
	// expired state while the ID metadata must still produce a visible ref.
	cache := newImageCache(1)
	tr := newImageTransformer(cache)
	png := testPNG(1440, 1440, 16)
	upload := kitty("a=T,f=100,q=2,C=1,c=47,r=20,i=3880550720", []byte(base64.StdEncoding.EncodeToString(png)))
	uploadRef := decodeRef(t, tr.feed(upload))
	if _, ok, expired := cache.get(uploadRef.Hash); ok || !expired {
		t.Fatalf("uploaded object ok=%v expired=%v, want typed expiry", ok, expired)
	}

	deletePlacements := kittyNoPayload("a=d,d=a,q=2")
	deleted := tr.feed(deletePlacements)
	if !bytes.HasPrefix(deleted, deletePlacements) || !bytes.Contains(deleted, []byte(`"action":"delete","all":true`)) {
		t.Fatalf("placement delete output=%q", deleted)
	}
	placement := kittyNoPayload("a=p,q=2,C=1,c=47,r=20,i=3880550720")
	placementRef := decodeRef(t, tr.feed(placement))
	if placementRef.Action != "put" || placementRef.Hash != uploadRef.Hash || placementRef.ID != uploadRef.ID || placementRef.Cols != 47 || placementRef.Rows != 20 || placementRef.Bytes != len(png) {
		t.Fatalf("placement ref=%+v, upload ref=%+v", placementRef, uploadRef)
	}
}

func TestImageTransformerDataDeletesAndUnsupportedReuploadsInvalidateMetadata(t *testing.T) {
	newFixture := func(t *testing.T) (*imageTransformer, []byte) {
		t.Helper()
		tr := newImageTransformer(newImageCache(imageCacheLimit))
		png := testPNG(2, 2, 0)
		upload := kitty("a=T,f=100,q=2,C=1,c=2,r=2,i=77", []byte(base64.StdEncoding.EncodeToString(png)))
		_ = decodeRef(t, tr.feed(upload))
		return tr, kittyNoPayload("a=p,q=2,C=1,c=2,r=2,i=77")
	}
	for name, invalidate := range map[string][]byte{
		"delete image":         kittyNoPayload("a=d,d=I,i=77,q=2"),
		"delete all data":      kittyNoPayload("a=d,d=A,q=2"),
		"unsupported reupload": kitty("a=T,t=f,f=100,q=2,C=1,c=2,r=2,i=77", []byte("L3RtcC94LnBuZw==")),
	} {
		t.Run(name, func(t *testing.T) {
			tr, placement := newFixture(t)
			gotInvalidate := tr.feed(invalidate)
			if !bytes.HasPrefix(gotInvalidate, invalidate) {
				t.Fatalf("native invalidation bytes changed: %q", gotInvalidate)
			}
			if name == "unsupported reupload" {
				cleanup := decodeRef(t, gotInvalidate[len(invalidate):])
				if cleanup.Action != "delete" || cleanup.ID != 77 {
					t.Fatalf("cleanup=%+v", cleanup)
				}
			}
			if got := transformAll(t, tr, placement); !bytes.Equal(got, placement) {
				t.Fatalf("stale placement translated: %q", got)
			}
		})
	}
}

func TestImageTransformerMalformedMultipartReuploadCleansPriorOverlay(t *testing.T) {
	tr := newImageTransformer(newImageCache(imageCacheLimit))
	oldPNG := testPNG(2, 2, 0)
	oldUpload := kitty("a=T,f=100,q=2,C=1,c=2,r=2,i=77", []byte(base64.StdEncoding.EncodeToString(oldPNG)))
	_ = decodeRef(t, tr.feed(oldUpload))

	newEncoded := base64.StdEncoding.EncodeToString(testPNG(3, 3, 0))
	first := kitty("a=T,f=100,q=2,C=1,c=3,r=3,i=77,m=1", []byte(newEncoded[:8]))
	if got := tr.feed(first); len(got) != 0 {
		t.Fatalf("first multipart output=%q", got)
	}
	last := kitty("m=0", []byte("!!!!"))
	got := tr.feed(last)
	native := append(append([]byte(nil), first...), last...)
	if !bytes.HasPrefix(got, native) {
		t.Fatalf("native multipart bytes changed: %q", got)
	}
	cleanup := decodeRef(t, got[len(native):])
	if cleanup.Action != "delete" || cleanup.ID != 77 {
		t.Fatalf("cleanup=%+v", cleanup)
	}
}

func TestImageTransformerActualPiCroppedPlacement(t *testing.T) {
	tr := newImageTransformer(newImageCache(imageCacheLimit))
	png := testPNG(1440, 1440, 0)
	upload := kitty("a=T,f=100,q=2,C=1,c=47,r=20,i=3880550720", []byte(base64.StdEncoding.EncodeToString(png)))
	uploadRef := decodeRef(t, tr.feed(upload))

	// cropKittyImageLine(line, 5, 7) removes the original r and appends
	// y=floor(1440*5/20), h=ceil(1440*12/20)-y, r=7. Then
	// getKittyImagePlacement preserves those controls in this exact form.
	crop := kittyNoPayload("a=p,q=2,C=1,c=47,i=3880550720,y=360,h=504,r=7")
	cropRef := decodeRef(t, tr.feed(crop))
	if cropRef.Hash != uploadRef.Hash || cropRef.ID != uploadRef.ID || cropRef.Cols != 47 || cropRef.Rows != 7 || cropRef.Bytes != len(png) {
		t.Fatalf("crop ref=%+v upload=%+v", cropRef, uploadRef)
	}

	for _, invalid := range []string{
		"a=p,q=2,C=1,c=47,i=3880550720,y=-1,h=504,r=7",
		"a=p,q=2,C=1,c=47,i=3880550720,y=1200,h=504,r=7",
		"a=p,q=2,C=1,c=47,i=3880550720,y=360,r=7",
	} {
		raw := kittyNoPayload(invalid)
		if got := tr.feed(raw); !bytes.Equal(got, raw) {
			t.Fatalf("invalid crop translated: %q", got)
		}
	}
}

func TestImageMetadataMapIsBoundedLRU(t *testing.T) {
	metadata := newImageMetadataMap(2)
	metadata.put(imageMetadata{id: 1, hash: "one"})
	metadata.put(imageMetadata{id: 2, hash: "two"})
	if _, ok := metadata.get(1); !ok { // refresh 1; 2 becomes oldest
		t.Fatal("metadata 1 missing")
	}
	metadata.put(imageMetadata{id: 3, hash: "three"})
	if _, ok := metadata.get(2); ok || metadata.lru.Len() != 2 {
		t.Fatalf("metadata map did not evict LRU: entries=%d", metadata.lru.Len())
	}
}

func TestImageTransformerDeleteKeepsNativeCommandAndAddsMetadata(t *testing.T) {
	cases := []struct {
		header string
		id     uint32
		all    bool
	}{
		{"a=d,d=I,i=3880550720,q=2", 3880550720, false},
		{"a=d,d=A,q=2", 0, true},
		{"a=d,d=a,q=2", 0, true},
	}
	for _, tc := range cases {
		raw := kitty(tc.header, nil)
		got := transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), raw)
		if !bytes.HasPrefix(got, raw) {
			t.Fatalf("native delete was removed: %q", got)
		}
		ref := decodeRef(t, got[len(raw):])
		if ref.Action != "delete" || ref.ID != tc.id || ref.All != tc.all {
			t.Fatalf("delete ref=%+v", ref)
		}
	}
	unsupported := kitty("a=d,d=p,p=4,q=2", nil)
	if got := transformAll(t, newImageTransformer(newImageCache(imageCacheLimit)), unsupported); !bytes.Equal(got, unsupported) {
		t.Fatalf("unsupported delete changed: %q", got)
	}
}

func TestImageValidationBounds(t *testing.T) {
	for name, png := range map[string][]byte{
		"dimension": testPNG(imageDimensionLimit+1, 1, 0),
		"pixels":    testPNG(5000, 5000, 0),
		"magic":     []byte("not a png"),
	} {
		t.Run(name, func(t *testing.T) {
			if validPNG(png) {
				t.Fatal("invalid PNG accepted")
			}
		})
	}
}

func TestImageCacheLRUAndTypedExpiry(t *testing.T) {
	cache := newImageCache(6)
	a := cache.put([]byte("aaa"))
	if again := cache.put([]byte("aaa")); again != a || cache.lru.Len() != 1 || cache.bytes != 3 {
		t.Fatalf("dedup: hash=%q entries=%d bytes=%d", again, cache.lru.Len(), cache.bytes)
	}
	b := cache.put([]byte("bbb"))
	if _, ok, _ := cache.get(a); !ok { // refresh a, so b is the victim
		t.Fatal("a missing")
	}
	cache.put([]byte("ccc"))
	if _, ok, expired := cache.get(b); ok || !expired {
		t.Fatalf("b ok=%v expired=%v", ok, expired)
	}
	if _, ok, expired := cache.get(strings.Repeat("0", 64)); ok || expired {
		t.Fatalf("unknown hash ok=%v expired=%v", ok, expired)
	}
}

func TestRunnerImageHTTP(t *testing.T) {
	cache := newImageCache(imageCacheLimit)
	png := testPNG(1, 1, 0)
	hash := cache.put(png)
	s := &Server{images: cache}

	req := httptest.NewRequest(http.MethodGet, "/images/"+hash, nil)
	req.SetPathValue("hash", hash)
	rr := httptest.NewRecorder()
	s.handleImage(rr, req)
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), png) || rr.Header().Get("Content-Type") != "image/png" || rr.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rr.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("response: code=%d headers=%v body=%q", rr.Code, rr.Header(), rr.Body.Bytes())
	}

	req = httptest.NewRequest(http.MethodGet, "/images/BAD", nil)
	req.SetPathValue("hash", "BAD")
	rr = httptest.NewRecorder()
	s.handleImage(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_hash") {
		t.Fatalf("invalid response: %d %s", rr.Code, rr.Body.String())
	}

	tiny := newImageCache(1)
	expiredHash := tiny.put([]byte("too large"))
	s.images = tiny
	req = httptest.NewRequest(http.MethodGet, "/images/"+expiredHash, nil)
	req.SetPathValue("hash", expiredHash)
	rr = httptest.NewRecorder()
	s.handleImage(rr, req)
	if rr.Code != http.StatusGone || !strings.Contains(rr.Body.String(), "image_expired") {
		t.Fatalf("expired response: %d %s", rr.Code, rr.Body.String())
	}
}

func TestReplayBoundaryScopesMultipartWaitToRefsClients(t *testing.T) {
	s := &Server{
		imageTransform: &imageTransformer{transfer: &kittyTransfer{raw: []byte("in progress")}},
		ptyDone:        make(chan struct{}),
	}
	if !s.lockReplayBoundary(context.Background(), false) {
		t.Fatal("raw attach was blocked by refs-only multipart state")
	}
	s.mu.Unlock() // success returns with the publication lock held

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.lockReplayBoundary(ctx, true) {
		s.mu.Unlock()
		t.Fatal("refs attach crossed an incomplete multipart transfer")
	}
}

func TestReferenceReplayHasIndependentCheckpointBudget(t *testing.T) {
	var original, refs rawReplay
	original.invalidate() // model an original/base64 checkpoint exceeding its cap
	ref := encodeImageReference(imageReference{Version: 1, Action: "put", Hash: strings.Repeat("a", 64), ID: 1, Cols: 2, Rows: 3, Bytes: 4, Mime: "image/png"})
	frame := append([]byte("\x1b[?2026h\x1b[H\x1b[2J\x1b[3J"), ref...)
	frame = append(frame, replayESU...)
	refs.write(frame)
	if got := original.bytes(); got != nil {
		t.Fatalf("original replay unexpectedly valid: %q", got)
	}
	if got := refs.bytes(); !bytes.Contains(got, []byte("gmux-image")) || bytes.Contains(got, []byte("iVBOR")) {
		t.Fatalf("reference replay=%q", got)
	}
}
